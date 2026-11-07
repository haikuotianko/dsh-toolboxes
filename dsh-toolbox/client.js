/**
 * dsh-toolbox: manager page.
 * Tools (install/progress/cancel/sources/enable/uninstall) + scripts (run with args),
 * plus the live console of every command the toolbox is running right now.
 */
window.__ModuleLoader__.load({
  id: 'dsh-toolbox',
  factory(require) {
    const React = require('react');
    const h = React.createElement;
    const API = '/dsh-toolbox/api/v1';

    const C = {
      bg: 'var(--dsw-alias-bg-base)',
      text: 'var(--dsw-alias-label-primary)',
      subtle: 'var(--dsw-alias-label-secondary)',
      border: 'var(--dsw-alias-border-l2)',
      panel: 'var(--dsw-alias-bg-layer-1)',
      surface: 'var(--dsw-alias-bg-layer-2)',
      brand: 'var(--dsw-alias-brand-primary)',
      error: 'var(--dsw-alias-state-error-primary)',
    };
    const CATEGORIES = [['all', '全部'], ['media', '媒体'], ['net', '网络'], ['search', '搜索'], ['data', '数据'], ['dev', '开发'], ['ops', '运维'], ['docs', '文档']];

    const input = () => ({ font: 'inherit', fontSize: 12.5, padding: '5px 9px', borderRadius: 7, color: C.text, background: C.panel, border: '1px solid ' + C.border, outline: 'none' });
    const kb = (n) => Math.round((n || 0) / 1024) + ' KB';
    const rate = (n) => (n > 0 ? Math.round(n / 1024) + ' KB/s' : '-');
    const done = (p) => ['done', 'failed', 'cancelled'].indexOf(p) >= 0;

    // ---- command runs: what the agent (or this page) is executing right now ----
    const PHASE = { running: '运行中', done: '完成', failed: '失败', timeout: '超时', cancelled: '已取消', lost: '中断' };
    const isLive = (run) => !!run && run.phase === 'running';

    function humanTime(ms) {
      const total = Math.max(0, Math.round((ms || 0) / 1000));
      if (total < 60) return total + 's';
      const minutes = Math.floor(total / 60);
      if (minutes < 60) return minutes + 'm' + String(total % 60).padStart(2, '0') + 's';
      return Math.floor(minutes / 60) + 'h' + String(minutes % 60).padStart(2, '0') + 'm';
    }

    // Terminal-ish rendering: \r rewrites the current line (yt-dlp / curl
    // progress bars), \r\n keeps it. Keeps the last 500 lines only.
    function consoleLines(raw) {
      const lines = [];
      let line = '';
      let rewrite = false;
      const text = raw || '';
      for (let i = 0; i < text.length; i++) {
        const ch = text.charAt(i);
        if (ch === '\r') { rewrite = true; continue; }
        if (ch === '\n') {
          lines.push(line);
          line = '';
          rewrite = false;
          if (lines.length > 500) lines.shift();
          continue;
        }
        if (rewrite) { line = ''; rewrite = false; }
        line += ch;
      }
      if (line) lines.push(line);
      return lines;
    }

    async function request(path, init) {
      const r = await fetch(API + path, init);
      const text = await r.text();
      let data;
      try { data = text ? JSON.parse(text) : {}; } catch (e) { data = { error: text }; }
      if (!r.ok) throw new Error(data.error || r.statusText);
      return data;
    }
    const post = (path, body) => request(path, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) });

    function Btn(props) {
      return h('button', {
        type: 'button', onClick: props.onClick, disabled: props.disabled,
        style: { font: 'inherit', fontSize: 12.5, padding: '4px 10px', borderRadius: 7, cursor: props.disabled ? 'default' : 'pointer', opacity: props.disabled ? 0.5 : 1, color: props.tone === 'danger' ? C.error : C.text, background: 'transparent', border: '1px solid ' + C.border },
      }, props.children);
    }
    const Box = (props) => h('div', { style: Object.assign({ border: '1px solid ' + C.border, borderRadius: 10, padding: '10px 12px', marginBottom: 8, background: C.bg }, props.style) }, props.children);
    function Section(props) {
      return h('div', { style: { marginTop: props.top || 20 } },
        h('div', { style: { display: 'flex', alignItems: 'baseline', gap: 8, cursor: 'pointer', padding: '6px 9px', borderRadius: 7, border: '1px solid ' + (props.open ? 'transparent' : C.border), background: props.open ? 'transparent' : C.panel }, onClick: props.onToggle },
          h('span', { style: { color: C.subtle, width: 10 } }, props.open ? 'v' : '>'),
          h('h2', { style: { margin: 0, fontSize: 14, fontWeight: 600 } }, props.title),
          h('span', { style: { fontSize: 11.5, color: C.subtle, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } }, props.summary)),
        props.open ? h('div', { style: { marginTop: 8 } }, props.children) : null);
    }
    const Meta = (props) => (props.value ? h('div', { style: { display: 'flex', gap: 8, fontSize: 12, marginTop: 3 } }, h('span', { style: { color: C.subtle, minWidth: 64 } }, props.label), h('span', { style: { wordBreak: 'break-all' } }, props.value)) : null);

    function Progress(props) {
      const job = props.job;
      const pct = job.total > 0 ? Math.min(100, Math.round((job.received / job.total) * 100)) : 0;
      const label = { measure: '测速中', download: '下载 ' + kb(job.received) + (job.total ? ' / ' + kb(job.total) : '') + ' ' + rate(job.speedBps), extract: '解包中', probe: '探测版本', queued: '排队', cancelled: '已取消(断点保留)', failed: '失败', done: '完成' }[job.phase] || job.phase;
      return h(Box, { style: { borderColor: C.brand } },
        h('div', { style: { display: 'flex', gap: 10, alignItems: 'center' } },
          h('code', { style: { fontWeight: 600 } }, job.tool),
          h('span', { style: { fontSize: 12, color: C.subtle } }, label),
          h('div', { style: { marginLeft: 'auto' } }, done(job.phase) ? null : h(Btn, { onClick: props.onCancel, tone: 'danger' }, '取消'))),
        job.phase === 'download' || job.phase === 'done' ? h('div', { style: { height: 6, borderRadius: 4, background: C.surface, marginTop: 8, overflow: 'hidden' } }, h('div', { style: { height: '100%', width: pct + '%', background: C.brand } })) : null,
        job.error ? h('div', { style: { fontSize: 11.5, color: C.error, marginTop: 6 } }, job.error) : null);
    }

    // RunConsole tails one run's output from the toolbox API. It polls only
    // while the command is running, so a finished console costs one request.
    function RunConsole(props) {
      const run = props.run;
      const [text, setText] = React.useState('');
      const [partial, setPartial] = React.useState(false);
      const [error, setError] = React.useState('');
      const box = React.useRef(null);
      const stick = React.useRef(true);

      React.useEffect(() => {
        let live = true;
        let timer = null;
        setText('');
        setError('');
        const load = () => {
          request('/runlog?id=' + encodeURIComponent(run.id) + '&tail=24000').then((v) => {
            if (!live) return;
            setText(v.output || '');
            setPartial(!!v.partial);
            if (v.phase === 'running') timer = setTimeout(load, 700);
          }, (e) => {
            if (!live) return;
            setError(String(e.message || e));
            timer = setTimeout(load, 2000);
          });
        };
        load();
        return () => { live = false; if (timer) clearTimeout(timer); };
      }, [run.id, run.phase]);

      React.useEffect(() => {
        const node = box.current;
        if (node && stick.current) node.scrollTop = node.scrollHeight;
      }, [text]);

      const lines = consoleLines(text).slice(-400);
      const empty = lines.length ? null : (error || (isLive(run) ? '等待输出…' : '（没有输出）'));
      return h('div', { style: { marginTop: 6 } },
        partial ? h('div', { style: { fontSize: 11, color: C.subtle, marginBottom: 3 } }, '只显示最后一段输出') : null,
        h('pre', {
          ref: box,
          onScroll: (e) => {
            const node = e.target;
            stick.current = node.scrollHeight - node.scrollTop - node.clientHeight < 24;
          },
          style: { margin: 0, padding: '7px 9px', borderRadius: 7, background: C.panel, color: C.text, font: '11.5px/1.45 ui-monospace, Consolas, monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: props.maxHeight || 180, overflow: 'auto' },
        }, empty || lines.join('\n')));
    }

    // RunCard is one command line: tool, args, state, elapsed, and its console.
    function RunCard(props) {
      const run = props.run;
      const [open, setOpen] = React.useState(isLive(run));
      const tone = isLive(run) ? C.brand : (run.phase === 'done' && !run.exitCode ? C.subtle : C.error);
      const exit = run.phase === 'done' && run.exitCode ? ' · exit ' + run.exitCode : '';
      const args = (run.args || []).join(' ');
      return h(Box, { style: { borderColor: isLive(run) && open ? C.brand : C.border } },
        h('div', { style: { display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }, onClick: () => setOpen(!open) },
          h('span', { style: { color: C.subtle, width: 10 } }, open ? 'v' : '>'),
          h('code', { style: { fontWeight: 600 } }, run.tool),
          h('span', { style: { fontSize: 11.5, color: C.subtle, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: 'ui-monospace, monospace' } }, args || '（无参数）'),
          h('span', { style: { fontSize: 11.5, color: tone } }, (PHASE[run.phase] || run.phase) + exit),
          h('span', { style: { fontSize: 11, color: C.subtle } }, humanTime(run.elapsedMs)),
          run.origin === 'agent' ? h('span', { style: { fontSize: 10.5, color: C.subtle, border: '1px solid ' + C.border, borderRadius: 4, padding: '0 4px' } }, 'AI') : null,
          isLive(run) ? h(Btn, { tone: 'danger', onClick: (e) => { e.stopPropagation(); props.onCancel(run.id); } }, '取消') : null),
        open ? h(RunConsole, { run: run, maxHeight: props.maxHeight }) : null,
        run.error && run.phase !== 'cancelled' ? h('div', { style: { fontSize: 11.5, color: C.error, marginTop: 4 } }, run.error) : null);
    }

    function Scripts() {
      const [list, setList] = React.useState(null);
      const [draft, setDraft] = React.useState(null);
      const [runFor, setRunFor] = React.useState('');
      const [args, setArgs] = React.useState('[]');
      const [out, setOut] = React.useState('');
      const [err, setErr] = React.useState('');
      const [open, setOpen] = React.useState(false);

      const load = React.useCallback(() => { request('/scripts').then((v) => setList(v.scripts || []), (e) => setErr(String(e.message || e))); }, []);
      React.useEffect(() => { load(); }, [load]);

      const blank = { name: '', title: '', description: '', usage: '', language: 'python', content: '' };
      const openRun = (s) => {
        let a = '[]';
        const m = /"args"\s*:\s*(\[[^]*?\])/.exec(s.usage || '');
        if (m) { try { a = JSON.stringify(JSON.parse(m[1])); } catch (e) { a = m[1]; } }
        setRunFor(s.id); setArgs(a); setOut('');
      };
      const run = (id) => {
        let a;
        try { a = JSON.parse(args || '[]'); } catch (e) { setOut('参数不是合法 JSON 数组'); return; }
        if (!Array.isArray(a)) { setOut('参数必须是 JSON 数组'); return; }
        post('/run', { tool: id, args: a }).then(
          (r) => setOut('exit=' + r.exitCode + '\n' + (r.stdout || '') + (r.stderr ? '\n' + r.stderr : '')),
          (e) => setOut(String(e.message || e)));
      };
      const field = (label, key, rows) => h('div', { style: { marginBottom: 6 } },
        h('div', { style: { fontSize: 11.5, color: C.subtle, marginBottom: 2 } }, label),
        h('textarea', { value: draft[key] || '', rows: rows, onChange: (e) => setDraft(Object.assign({}, draft, { [key]: e.target.value })), style: Object.assign(input(), { width: '100%', boxSizing: 'border-box', resize: 'vertical' }) }));

      return h('div', { style: { marginTop: 24 } },
        h('div', { style: { display: 'flex', alignItems: 'baseline', gap: 10 } },
          h('span', { style: { color: C.subtle, width: 10, cursor: 'pointer' }, onClick: () => setOpen(!open) }, open ? 'v' : '>'),
          h('h2', { style: { fontSize: 14, fontWeight: 600, margin: 0, cursor: 'pointer' }, onClick: () => setOpen(!open) }, '脚本 · ' + (list ? list.length : '...')),
          h('span', { style: { fontSize: 11.5, color: C.subtle, flex: 0, maxWidth: 420, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } }, (list || []).map((x) => x.id).join(', ')),
          h('span', { style: { fontSize: 12, color: C.subtle } }, 'AI 归档的脚本；运行前填参数'),
          h('div', { style: { marginLeft: 'auto' } }, h(Btn, { onClick: () => setDraft(Object.assign({}, blank)) }, '+ 新建'))),
        err ? h('div', { style: { color: C.error, fontSize: 11.5, marginTop: 6 } }, err) : null,
        draft ? h(Box, { style: { marginTop: 8 } },
          field('id', 'name', 1), field('标题', 'title', 1), field('用途', 'description', 1), field('调用示例', 'usage', 2),
          h('div', { style: { marginBottom: 6 } }, h('div', { style: { fontSize: 11.5, color: C.subtle, marginBottom: 2 } }, '语言'),
            h('select', { value: draft.language, onChange: (e) => setDraft(Object.assign({}, draft, { language: e.target.value })), style: input() },
              h('option', { value: 'python' }, 'python'), h('option', { value: 'node' }, 'node'), h('option', { value: 'powershell' }, 'powershell'))),
          field('内容', 'content', 10),
          h('div', { style: { display: 'flex', gap: 6 } },
            h(Btn, { onClick: () => post('/script', draft).then(() => { setDraft(null); load(); }, (e) => setErr(String(e.message || e))), disabled: !(draft.name || '').trim() || !(draft.content || '').trim() }, '保存'),
            h(Btn, { onClick: () => setDraft(null) }, '取消'))) : null,
        open ? (list || []).map((s) => h(Box, { key: s.id },
          h('div', { style: { display: 'flex', gap: 10, alignItems: 'center' } },
            h('code', { style: { fontWeight: 600 } }, s.id),
            h('span', { style: { fontSize: 11.5, color: C.subtle } }, s.language),
            h('span', { style: { fontSize: 12, color: C.subtle, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } }, s.description),
            h(Btn, { onClick: () => setDraft(Object.assign({}, blank, s)) }, '编辑'),
            h(Btn, { onClick: () => (runFor === s.id ? setRunFor('') : openRun(s)) }, runFor === s.id ? '收起' : '运行'),
            h(Btn, { tone: 'danger', onClick: () => request('/script', { method: 'DELETE', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ tool: s.id }) }).then(load, (e) => setErr(String(e.message || e))) }, '删除')),
          runFor === s.id ? h('div', { style: { marginTop: 8 } },
            h('div', { style: { fontSize: 11.5, color: C.subtle, marginBottom: 4 } }, '参数 JSON 数组（按 usage 预填）'),
            h('textarea', { value: args, rows: 3, onChange: (e) => setArgs(e.target.value), style: Object.assign(input(), { width: '100%', boxSizing: 'border-box', fontFamily: 'ui-monospace, monospace' }) }),
            h('div', { style: { display: 'flex', gap: 6, marginTop: 6 } }, h(Btn, { onClick: () => run(s.id) }, '执行'), h(Btn, { onClick: () => setRunFor('') }, '取消')),
            out ? h('pre', { style: { marginTop: 6, padding: 6, borderRadius: 6, background: C.panel, fontSize: 11, whiteSpace: 'pre-wrap', maxHeight: 160, overflow: 'auto' } }, out) : null) : null)) : null);
    }

    // ConnectSection hands the user the exact MCP row for this machine: the page
    // working only proves the loopback API; the agent needs its own MCP client.
    function ConnectSection() {
      const [info, setInfo] = React.useState(null);
      const [open, setOpen] = React.useState(false);
      const [copied, setCopied] = React.useState('');
      const area = React.useRef(null);
      React.useEffect(() => { request('/launcher').then(setInfo, () => {}); }, []);
      const text = info && info.json ? JSON.stringify(info.json, null, 2) : '';
      const copy = () => {
        const finish = () => { setCopied('已复制'); setTimeout(() => setCopied(''), 2500); };
        const node = area.current;
        const fallback = () => { if (node) { node.select(); document.execCommand('copy'); finish(); } };
        if (!text) return;
        if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(finish, fallback);
        else fallback();
      };
      return h(Section, { top: 24, title: '接入 AI（MCP）', summary: info ? info.command : '让 agent 能调用这些工具', open: open, onToggle: () => setOpen(!open) },
        h('div', { style: { fontSize: 11.5, color: C.subtle, marginBottom: 6 } }, '管理页能跑 ≠ AI 能用。把下面的配置加进你的 MCP 客户端（Claude Code / opencode 的配置文件，或 DSH 的 MCP 设置），也可以运行一次 toolbox -setup 让 DSH 自己写：'),
        h('pre', { style: { margin: 0, padding: '8px 10px', borderRadius: 7, background: C.panel, fontSize: 11, whiteSpace: 'pre-wrap', maxHeight: 180, overflow: 'auto' } }, text || '加载中…'),
        h('div', { style: { display: 'flex', gap: 8, alignItems: 'center', marginTop: 6, fontSize: 11, color: C.subtle } },
          h(Btn, { onClick: copy, disabled: !text }, '复制配置'),
          copied ? h('span', { style: { fontSize: 11.5, color: C.subtle } }, copied) : null,
          h('span', null, '工具箱目录：' + (info ? info.root : ''))),
        h('textarea', { ref: area, readOnly: true, value: text, style: { position: 'absolute', left: -9999, width: 1, height: 1, opacity: 0 } }));
    }

    const HINT_TEXT = "我在用工具箱 MCP（工具：toolbox_list / toolbox_run / toolbox_runtimes / toolbox_save_script / toolbox_install）。\n请把下面这些规则写进项目根目录的 AGENTS.md：文件不存在就新建，已存在就追加到末尾（不要覆盖原有内容）。写完后告诉我你改了哪个文件。\n\n## 动手前先看工具箱\n- 需要跑命令或写脚本时，先调用 toolbox_list；能覆盖的通用活优先 toolbox_run，没装先问我要不要装（别一上来就写脚本）。\n- 写脚本前先调用 toolbox_runtimes 确认本机有哪些语言：密集计算/大文件用 Go 或 Rust，文本与数据加工用 Python，Windows 系统操作用 PowerShell 5.1，JSON/HTTP 可用 Node。\n- 脚本写完后如果它通用（输入输出清晰、无项目私有路径或常量），用 toolbox_save_script 存进工具箱，并告诉我 id 与调用方式；一次性的不要存。\n- 工具已经给出结果时不要用另一套实现再复核一遍；只有结果可疑才抽查。\n";

    function HintSection() {
      const [open, setOpen] = React.useState(false);
      const [copied, setCopied] = React.useState('');
      const ref = React.useRef(null);
      const copy = () => {
        const done = () => { setCopied('已复制'); setTimeout(() => setCopied(''), 2500); };
        const node = ref.current;
        const fallback = () => { if (node) { node.select(); document.execCommand('copy'); done(); } };
        if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(HINT_TEXT).then(done, fallback);
        else fallback();
      };
      return h(Section, { top: 24, title: 'AI 不主动用工具箱？', summary: '复制一段话发给 AI，让它自己写进 AGENTS.md', open: open, onToggle: () => setOpen(!open) },
        h('div', { style: { fontSize: 11.5, color: C.subtle, marginBottom: 6 } }, '复制下面这段，直接发给你的 AI；它会自己写进 AGENTS.md：'),
        h('textarea', { ref: ref, readOnly: true, value: HINT_TEXT, rows: 9, style: Object.assign({}, input(), { width: '100%', boxSizing: 'border-box', fontFamily: 'ui-monospace, monospace', fontSize: 11.5 }) }),
        h('div', { style: { display: 'flex', gap: 8, alignItems: 'center', marginTop: 6 } },
          h(Btn, { onClick: copy }, '复制这段规则'),
          copied ? h('span', { style: { fontSize: 11.5, color: C.subtle } }, copied) : null));
    }

    function Page() {
      const [state, setState] = React.useState(null);
      const [err, setErr] = React.useState('');
      const [job, setJob] = React.useState(null);
      const [open, setOpen] = React.useState('');
      const [detail, setDetail] = React.useState({});
      const [speeds, setSpeeds] = React.useState({});
      const [cat, setCat] = React.useState('all');
      const [env, setEnv] = React.useState(null);
      const [customId, setCustomId] = React.useState('');
      const [cardUrl, setCardUrl] = React.useState({});
      const [customUrl, setCustomUrl] = React.useState('');
      const [notice, setNotice] = React.useState('');
      const [openInstalled, setOpenInstalled] = React.useState(true);
      const [openAvailable, setOpenAvailable] = React.useState(false);
      const [openEnv, setOpenEnv] = React.useState(false);
      const [runs, setRuns] = React.useState([]);
      const [openRuns, setOpenRuns] = React.useState(true);

      const refresh = React.useCallback(() => { request('/state').then(setState, (e) => setErr(String(e.message || e))); }, []);
      React.useEffect(() => { refresh(); }, [refresh]);

      // Poll the run list: fast while something executes, lazily when idle.
      // The agent talks to an MCP process of its own, so this reads the shared
      // <root>/runs directory instead of any in-memory state.
      React.useEffect(() => {
        let mounted = true;
        let timer = null;
        const load = () => {
          request('/runs').then((v) => {
            if (!mounted) return;
            const list = v.runs || [];
            setRuns(list);
            timer = setTimeout(load, list.some(isLive) ? 800 : 3000);
          }, () => {
            if (mounted) timer = setTimeout(load, 5000);
          });
        };
        load();
        return () => { mounted = false; if (timer) clearTimeout(timer); };
      }, []);

      React.useEffect(() => {
        if (!job || done(job.phase)) return undefined;
        const t = setTimeout(() => {
          request('/job?id=' + encodeURIComponent(job.id)).then((next) => {
            setJob(next);
            if (done(next.phase)) { if (next.phase === 'failed') setErr(next.error || '安装失败'); refresh(); }
          }, (e) => setErr(String(e.message || e)));
        }, 400);
        return () => clearTimeout(t);
      }, [job, refresh]);

      const start = (id, url, archive) => {
        setErr('');
        post('/install', { tool: id, url: url, archive: archive }).then((s) => setJob({ id: s.job, tool: id, phase: 'queued', received: 0, total: 0 }), (e) => setErr(String(e.message || e)));
      };
      const loadDetail = (id) => { if (!detail[id]) request('/tool?id=' + encodeURIComponent(id)).then((v) => setDetail((p) => Object.assign({}, p, { [id]: v })), () => {}); };
      const toggle = (id) => { setOpen(open === id ? '' : id); loadDetail(id); };
      const cancelRun = (id) => { post('/runs/cancel', { id }).then(null, (e) => setErr(String(e.message || e))); };
      const clearRuns = () => { post('/runs/clear', {}).then(() => setRuns((list) => list.filter(isLive)), (e) => setErr(String(e.message || e))); };

      const row = (t) => {
        const isOpen = open === t.id;
        const d = detail[t.id] || {};
        const mine = runs.filter((r) => r.tool === t.id);
        const liveCount = mine.filter(isLive).length;
        return h(Box, { key: t.id },
          h('div', { style: { display: 'flex', gap: 10, alignItems: 'center', cursor: 'pointer' }, onClick: () => toggle(t.id) },
            h('span', { style: { color: C.subtle, width: 10 } }, isOpen ? 'v' : '>'),
            h('code', { style: { fontWeight: 600 } }, t.id),
            h('span', { style: { color: C.subtle, fontSize: 12, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } }, t.description),
            liveCount ? h('span', { title: '正在运行的命令数', style: { fontSize: 11, padding: '1px 7px', borderRadius: 999, border: '1px solid ' + C.brand, color: C.brand } }, '运行中 ' + liveCount) : null,
            t.version ? h('span', { style: { fontSize: 11.5, color: C.subtle } }, t.version) : null,
            t.enabled === false ? h('span', { style: { fontSize: 11.5, color: C.error } }, '已停用') : null,
            h(Btn, { onClick: (e) => { e.stopPropagation(); post('/enable', { tool: t.id, enabled: t.enabled === false }).then(refresh, (x) => setErr(String(x.message || x))); } }, t.enabled === false ? '启用' : '停用'),
            h(Btn, { tone: 'danger', onClick: (e) => { e.stopPropagation(); post('/remove', { tool: t.id }).then((r) => { setNotice(t.id + ' 已卸载：' + (r.filesRemoved ? '文件已删除' : '仅删除记录，程序文件保留')); setDetail({}); refresh(); }, (x) => setErr(String(x.message || x))); } }, '卸载')),
          isOpen ? h('div', { style: { marginTop: 8, paddingTop: 8, borderTop: '1px solid ' + C.border } },
            h(Meta, { label: '用途', value: d.manifest && d.manifest.description }), h(Meta, { label: '用法', value: d.manifest && d.manifest.usage }),
            h(Meta, { label: '可执行', value: d.manifest && d.manifest.command }), h(Meta, { label: '来源', value: d.manifest && d.manifest.source }),
            d.readme ? h('pre', { style: { marginTop: 8, padding: 8, borderRadius: 6, background: C.panel, fontSize: 11.5, whiteSpace: 'pre-wrap', maxHeight: 200, overflow: 'auto' } }, d.readme) : null,
            mine.length ? h('div', { style: { marginTop: 10 } },
              h('div', { style: { fontSize: 12, fontWeight: 600, marginBottom: 4 } }, '最近运行 · ' + mine.length),
              mine.slice(0, 4).map((r) => h(RunCard, { key: r.id, run: r, onCancel: cancelRun }))) : null) : null);
      };
      const card = (t) => {
        const isOpen = open === t.id;
        const d = detail[t.id] || {};
        const srcs = (d.sources && d.sources.length ? d.sources : t.sources) || [];
        const measured = speeds[t.id];
        return h(Box, { key: t.id },
          h('div', { style: { display: 'flex', gap: 10, alignItems: 'center', cursor: 'pointer' }, onClick: () => toggle(t.id) },
            h('span', { style: { color: C.subtle, width: 10 } }, isOpen ? 'v' : '>'),
            h('code', { style: { fontWeight: 600 } }, t.id),
            h('span', { style: { fontSize: 12, color: C.subtle, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } }, t.description),
            h(Btn, { onClick: (e) => { e.stopPropagation(); start(t.id); }, disabled: !!(job && !done(job.phase)) }, '安装')),
          isOpen ? h('div', { style: { marginTop: 8, paddingTop: 8, borderTop: '1px solid ' + C.border } },
            h(Meta, { label: '用法', value: d.catalog && d.catalog.usage }),
            h('div', { style: { display: 'flex', gap: 6, marginTop: 8 } },
              h('div', { style: { fontSize: 12, fontWeight: 600 } }, '下载源'),
              h('div', { style: { marginLeft: 'auto' } }, h(Btn, { onClick: () => { setSpeeds((p) => Object.assign({}, p, { [t.id]: 'probing' })); request('/sources?tool=' + encodeURIComponent(t.id)).then((r) => setSpeeds((p) => Object.assign({}, p, { [t.id]: r.sources })), (e) => setErr(String(e.message || e))); } }, '全部测速'))),
            t.platformMissing ? h('div', { style: { fontSize: 11.5, color: C.error, marginTop: 6 } }, '本系统没有预设下载源：请自己填一个链接，或用下面的本机程序导入') : null,
            srcs.length === 0 ? h('div', { style: { display: 'flex', gap: 6, marginTop: 6 } },
              h('input', {
                placeholder: 'https 直链（自行填写）',
                value: cardUrl[t.id] || '',
                onChange: (e) => setCardUrl(Object.assign({}, cardUrl, { [t.id]: e.target.value })),
                style: Object.assign(input(), { flex: 1 }),
              }),
              h(Btn, { disabled: !(cardUrl[t.id] || '').trim(), onClick: () => start(t.id, (cardUrl[t.id] || '').trim()) }, '用这个链接装')) : null,
            srcs.map((s, i) => {
              const one = Array.isArray(measured) ? measured.filter((m) => m.url === s.url)[0] : null;
              return h('div', { key: i, style: { display: 'flex', gap: 8, alignItems: 'center', marginTop: 4, fontSize: 11.5 } },
                h('span', { style: { flex: 1, color: C.subtle, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } }, s.url),
                h('span', { style: { minWidth: 76, textAlign: 'right', color: one && one.error ? C.error : C.subtle } }, one ? (one.error ? '不可用' : rate(one.bps)) : '未测'),
                h(Btn, { onClick: () => request('/sources?tool=' + encodeURIComponent(t.id) + '&url=' + encodeURIComponent(s.url)).then((r) => setSpeeds((p) => Object.assign({}, p, { [t.id]: (Array.isArray(p[t.id]) ? p[t.id] : []).concat(r.sources) })), (e) => setErr(String(e.message || e))) }, '测速'),
                h(Btn, { onClick: () => start(t.id, s.url, s.archive) }, '装'));
            }),
            h('div', { style: { display: 'flex', gap: 6, marginTop: 10 } },
              h('input', { placeholder: '本机程序路径', value: customUrl, onChange: (e) => setCustomUrl(e.target.value), style: Object.assign(input(), { flex: 1 }) }),
              h(Btn, { onClick: () => post('/import', { tool: t.id, path: customUrl }).then(() => { setCustomUrl(''); setDetail({}); refresh(); }, (e) => setErr(String(e.message || e))) }, '导入本机程序'))) : null);
      };

      if (!state) return h('div', { style: { padding: 24, color: C.subtle } }, err || '加载中');
      const running = !!(job && !done(job.phase));
      const list = state.available.filter((t) => cat === 'all' || (t.category || 'dev') === cat);
      const liveRuns = runs.filter(isLive);
      const shownRuns = liveRuns.concat(runs.filter((r) => !isLive(r)).slice(0, 4));

      return h('div', { style: { boxSizing: 'border-box', height: '100%', overflow: 'auto', padding: '18px 22px 40px', color: C.text, background: C.bg, fontSize: 13 } },
        h('div', { style: { display: 'flex', gap: 12, alignItems: 'center' } },
          h('h1', { style: { margin: 0, fontSize: 17, fontWeight: 700 } }, '工具箱'),
          h('span', { style: { fontSize: 12, color: C.subtle } }, state.platform + ' · ' + state.root),
          h('div', { style: { marginLeft: 'auto', display: 'flex', gap: 10, alignItems: 'center' } },
            liveRuns.length ? h('span', { style: { fontSize: 12, color: C.brand } }, '● ' + liveRuns.length + ' 条命令运行中') : null,
            h(Btn, { onClick: refresh, disabled: running }, '刷新'))),
        runs.length ? h('div', { style: { marginTop: 12 } },
          h('div', { style: { display: 'flex', alignItems: 'baseline', gap: 8, cursor: 'pointer', padding: '6px 9px', borderRadius: 7, border: '1px solid ' + C.border, background: C.panel }, onClick: () => setOpenRuns(!openRuns) },
            h('span', { style: { color: C.subtle, width: 10 } }, openRuns ? 'v' : '>'),
            h('h2', { style: { margin: 0, fontSize: 14, fontWeight: 600 } }, '命令运行 · ' + (liveRuns.length ? liveRuns.length + ' 运行中' : runs.length + ' 条记录')),
            h('span', { style: { fontSize: 11.5, color: C.subtle, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } }, liveRuns.length ? liveRuns.map((r) => r.tool).join(', ') : '最近跑过的命令与输出'),
            h('div', { style: { marginLeft: 'auto' } }, h(Btn, { onClick: (e) => { e.stopPropagation(); clearRuns(); } }, '清除已结束'))),
          openRuns ? h('div', { style: { marginTop: 8 } }, shownRuns.map((r) => h(RunCard, { key: r.id, run: r, onCancel: cancelRun }))) : null) : null,
        h('div', { style: { display: 'flex', gap: 10, alignItems: 'center', marginTop: 12, padding: '8px 12px', borderRadius: 8, border: '1px solid ' + C.border, background: C.panel } },
          h('span', { style: { color: C.subtle, width: 10, cursor: 'pointer' }, onClick: () => setOpenEnv(!openEnv) }, openEnv ? 'v' : '>'),
          h('span', { style: { fontSize: 12, fontWeight: 600, cursor: 'pointer' }, onClick: () => setOpenEnv(!openEnv) }, '本机环境'),
          h('span', { style: { fontSize: 12, color: C.subtle, flex: 1, cursor: 'pointer', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }, onClick: () => setOpenEnv(!openEnv) }, Array.isArray(env) ? '发现 ' + env.length + ' 个：' + env.map((x) => x.id).join(', ') : '检查 PATH 里已有哪些预设工具'),
          h(Btn, { onClick: () => request('/env').then((r) => { setEnv(r.hits); setOpenEnv(true); }, (e) => setErr(String(e.message || e))) }, '检查')),
        Array.isArray(env) && openEnv ? env.map((x) => h('div', { key: x.id + x.path, style: { display: 'flex', gap: 10, alignItems: 'flex-start', fontSize: 11.5, padding: '6px 0', borderBottom: '1px solid ' + C.border } },
          h('div', { style: { flex: 1, minWidth: 0 } },
            h('div', { style: { display: 'flex', gap: 8 } }, h('code', { style: { fontWeight: 600 } }, x.id), x.managed ? h('span', { style: { color: C.subtle } }, '已登记') : null),
            x.description ? h('div', { style: { color: C.subtle, marginTop: 2 } }, x.description) : null,
            h('div', { style: { color: C.subtle, marginTop: 2 } }, x.path)),
          x.managed ? null : h(Btn, { onClick: () => post('/adopt', { tool: x.id, path: x.path }).then(() => { setDetail({}); refresh(); }, (e) => setErr(String(e.message || e))) }, '使用系统版本'))) : null,
        err ? h('div', { style: { marginTop: 12, padding: '8px 12px', borderRadius: 8, border: '1px solid ' + C.error, color: C.error, whiteSpace: 'pre-wrap' } }, err) : null,
        notice ? h('div', { onClick: () => setNotice(''), style: { marginTop: 12, padding: '8px 12px', borderRadius: 8, background: C.panel, fontSize: 12, cursor: 'pointer' } }, notice) : null,
        job ? h('div', { style: { marginTop: 12 } }, h(Progress, { job: job, onCancel: () => post('/cancel', { job: job.id }).catch(() => {}) })) : null,
        h(Section, { title: '已安装 · ' + state.installed.length, summary: state.installed.map((t) => t.id).join(', ') || '(空)', open: openInstalled, onToggle: () => setOpenInstalled(!openInstalled) }),
        openInstalled ? state.installed.map(row) : null,
        h(Section, { top: 24, title: '可安装 · ' + state.available.length, summary: CATEGORIES.filter((pair) => pair[0] !== 'all').map((pair) => pair[1] + ' ' + state.available.filter((t) => (t.category || 'dev') === pair[0]).length).join(' · '), open: openAvailable, onToggle: () => setOpenAvailable(!openAvailable) }),
        openAvailable ? h('div', { style: { display: 'flex', flexWrap: 'wrap', gap: 6, margin: '6px 0 10px' } },
          CATEGORIES.map((pair) => {
            const key = pair[0];
            const count = key === 'all' ? state.available.length : state.available.filter((t) => (t.category || 'dev') === key).length;
            const isActive = cat === key;
            return h('button', { key: key, type: 'button', onClick: () => setCat(key), style: { font: 'inherit', fontSize: 12, padding: '3px 10px', borderRadius: 999, cursor: 'pointer', color: isActive ? C.brand : C.text, background: isActive ? C.surface : 'transparent', border: '1px solid ' + (isActive ? C.brand : C.border), fontWeight: isActive ? 600 : 400 } }, pair[1] + ' ' + count);
          })) : null,
        openAvailable ? list.map(card) : null,
        h('h2', { style: { fontSize: 14, fontWeight: 600, marginTop: 24 } }, '自定义链接安装'),
        h('div', { style: { display: 'flex', gap: 6 } },
          h('input', { placeholder: '工具 id', value: customId, onChange: (e) => setCustomId(e.target.value), style: input() }),
          h('input', { placeholder: 'https 直链', value: customUrl, onChange: (e) => setCustomUrl(e.target.value), style: Object.assign(input(), { flex: 1 }) }),
          h(Btn, { disabled: !customId.trim() || !customUrl.trim(), onClick: () => start(customId.trim(), customUrl.trim()) }, '下载并安装')),
        h(ConnectSection),
        h(Scripts),
        h(HintSection));
    }

    function Icon(props) {
      return h('svg', { viewBox: '0 0 16 16', width: props.size, height: props.size, fill: 'none', stroke: 'currentColor', strokeWidth: 1.3, strokeLinecap: 'round', strokeLinejoin: 'round', 'aria-hidden': true },
        h('rect', { x: 1.8, y: 5.6, width: 12.4, height: 7.9, rx: 1.4 }),
        h('path', { d: 'M5.6 5.6V4.1a1.3 1.3 0 0 1 1.3-1.3h2.2a1.3 1.3 0 0 1 1.3 1.3v1.5' }));
    }

    return {
      inject: ['slots'],
      apply(ctx) {
        ctx.slots.inject('sidebar.panellist', () => ctx.slots.register({ name: 'sidebar.panellist', id: 'dsh-toolbox', order: 30, label: () => '工具箱' }, Icon));
        ctx.slots.inject('main', () => ctx.slots.register({ name: 'main', key: 'dsh-toolbox' }, Page));
      },
    };
  },
});
