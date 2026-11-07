// Smoke test for dsh-toolbox/client.js without a browser: load the plugin
// factory with a tiny React shim, render Page, and render the new RunCard /
// RunConsole straight from the real source text. Also checks the console text
// formatter against yt-dlp style \r progress output.
import { readFileSync } from 'node:fs';

const source = readFileSync('dsh-toolbox/client.js', 'utf8');
let failures = 0;
const check = (ok, label, extra = '') => {
  if (!ok) failures++;
  console.log((ok ? 'PASS ' : 'FAIL ') + label + (extra ? ' -> ' + extra : ''));
};

// ---- consoleLines, extracted verbatim ----
const fnStart = source.indexOf('    function consoleLines(');
const fnEnd = source.indexOf('    async function request(');
if (fnStart < 0 || fnEnd < 0) throw new Error('consoleLines not found');
const consoleLines = new Function(source.slice(fnStart, fnEnd) + '\nreturn consoleLines;')();

const samples = [
  ['download progress collapses to the last line', '[download]   0.0% at 1.00MiB/s\r[download]  50.0% at 2.00MiB/s\r[download] 100.0% in 00:05', ['[download] 100.0% in 00:05']],
  ['crlf keeps the text', 'line one\r\nline two\r\n', ['line one', 'line two']],
  ['overwrite only after a following char', 'abc\rdone', ['done']],
  ['plain lines', 'a\nb\nc', ['a', 'b', 'c']],
];
for (const [label, input, expected] of samples) {
  const got = consoleLines(input);
  check(JSON.stringify(got) === JSON.stringify(expected), 'consoleLines: ' + label, JSON.stringify(got));
}

// ---- React shim ----
const react = {
  createElement(type, props, ...children) {
    const merged = Object.assign({}, props);
    merged.children = children.length ? children : (props && props.children != null ? [props.children] : []);
    return { type, props: merged };
  },
  useState(initial) { return [typeof initial === 'function' ? initial() : initial, () => {}]; },
  useEffect() {},
  useCallback(fn) { return fn; },
  useMemo(fn) { return fn(); },
  useRef(value) { return { current: value === undefined ? null : value }; },
};

function render(node, out = []) {
  if (node === null || node === undefined || node === false || node === true) return out;
  if (Array.isArray(node)) { node.forEach((child) => render(child, out)); return out; }
  if (typeof node === 'string' || typeof node === 'number') { out.push(String(node)); return out; }
  const props = node.props || {};
  if (typeof node.type === 'function') return render(node.type(props), out);
  if (typeof node.type === 'string') return render(props.children, out);
  return out;
}

// ---- load the plugin through its module loader ----
let definition = null;
new Function('window', source)({ __ModuleLoader__: { load(def) { definition = def; } } });
check(!!definition, 'plugin registers with __ModuleLoader__');

const components = {};
definition.factory(() => react).apply({
  slots: {
    inject(_name, fn) { fn(); },
    register(spec, component) { components[spec.name] = component; },
  },
});
check(typeof components.main === 'function', 'page component registered');
check(typeof components['sidebar.panellist'] === 'function', 'sidebar icon registered');

const page = render(components.main({})).join('|');
check(page.includes('加载中'), 'Page renders its loading state');

// ---- RunCard / RunConsole from the real source ----
const blockStart = source.indexOf('    // RunConsole tails');
const blockEnd = source.indexOf('    function Scripts()');
if (blockStart < 0 || blockEnd < 0) throw new Error('run components not found');
const C = new Proxy({}, { get: (_t, key) => 'var(--' + String(key) + ')' });
const h = react.createElement;
const buildRuns = new Function(
  'React', 'h', 'Box', 'Btn', 'C', 'PHASE', 'isLive', 'humanTime', 'consoleLines',
  source.slice(blockStart, blockEnd) + '\nreturn { RunCard, RunConsole };',
);
const { RunCard } = buildRuns(
  react, h,
  (props) => h('div', props, props.children),
  (props) => h('button', props, props.children),
  C,
  { running: '运行中', done: '完成', failed: '失败', timeout: '超时', cancelled: '已取消', lost: '中断' },
  (run) => !!run && run.phase === 'running',
  (ms) => Math.round((ms || 0) / 1000) + 's',
  consoleLines,
);

const live = { id: 'r1', tool: 'yt-dlp', args: ['-f', 'best', 'https://x/v'], origin: 'agent', phase: 'running', bytes: 120, elapsedMs: 4200 };
const liveText = render(RunCard({ run: live, onCancel: () => {} })).join('|');
check(liveText.includes('yt-dlp'), 'RunCard shows the tool');
check(liveText.includes('-f best https://x/v'), 'RunCard shows the args');
check(liveText.includes('运行中'), 'RunCard shows a live phase');
check(liveText.includes('4s'), 'RunCard shows elapsed time');
check(liveText.includes('AI'), 'RunCard marks agent runs');
check(liveText.includes('取消'), 'RunCard offers cancel while live');
check(liveText.includes('等待输出'), 'RunConsole waits for output while live');

const failed = { id: 'r2', tool: 'ffmpeg', args: [], phase: 'failed', exitCode: 1, error: 'timed out after 2m0s', elapsedMs: 120000 };
const failedText = render(RunCard({ run: failed, onCancel: () => {} })).join('|');
check(failedText.includes('失败'), 'RunCard shows a failed phase');
check(failedText.includes('timed out'), 'RunCard shows the error');
check(!failedText.includes('取消'), 'RunCard hides cancel once settled');

const cancelled = { id: 'r3', tool: 'curl', args: [], phase: 'cancelled', exitCode: 1, error: '已取消', elapsedMs: 800 };
const cancelledText = render(RunCard({ run: cancelled, onCancel: () => {} })).join('|');
check(cancelledText.includes('已取消'), 'RunCard shows the cancelled phase');

console.log(failures === 0 ? 'ALL PASS' : failures + ' FAILURE(S)');
process.exit(failures === 0 ? 0 : 1);
