/**
 * dsh-toolbox — Host half: the manager page's backend only.
 *
 * The agent-facing tools come from a separate MCP client row (see
 * cordis.patch.yml), so the same Go server also works in opencode / Claude Code
 * / Codex without this plugin.
 *
 * This half keeps one HTTP instance of that binary alive on loopback and
 * proxies the page's `/dsh-toolbox/api/v1/*` calls to it, so the browser talks
 * to a single same origin and needs neither CORS nor a token.
 *
 * Where the binary comes from, in order:
 *   1. TOOLBOX_BIN (an explicit path you built yourself)
 *   2. bin/* shipped next to this file (a local build; not part of the npm tarball)
 *   3. the matching GitHub Release asset, downloaded once and cached under
 *      <toolbox root>/bin/toolbox-<version>[.exe]
 */
import { spawn, spawnSync } from 'node:child_process'
import crypto from 'node:crypto'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
// Versioned so a stale registration from an earlier module generation cannot
// collide with this one; the page uses the same constant.
const API_PREFIX = '/dsh-toolbox/api/v1'
const REPO = 'haikuotianko/dsh-toolboxes'

export const inject = ['webServer']

let serverPort = 0
let serverChild = null
let starting = null

function toolboxRoot() {
  if (process.env.TOOLBOX_ROOT) return process.env.TOOLBOX_ROOT
  const home = process.env.DSH_HOME || path.join(os.homedir(), '.dsh')
  return path.join(home, 'dsh-toolbox')
}

function packageVersion() {
  try {
    return JSON.parse(fs.readFileSync(path.join(HERE, 'package.json'), 'utf8')).version
  } catch {
    return '0.0.0'
  }
}

/** Release tag this package expects, e.g. v0.2.0. */
function releaseTag() {
  const tag = process.env.TOOLBOX_VERSION || packageVersion()
  return tag.startsWith('v') ? tag : 'v' + tag
}

/** Release asset for this machine, e.g. toolbox-windows-amd64.zip. */
function assetName() {
  const goos = os.platform() === 'win32' ? 'windows' : os.platform() === 'darwin' ? 'darwin' : 'linux'
  const goarch = os.arch() === 'x64' ? 'amd64' : os.arch()
  return `toolbox-${goos}-${goarch}.zip`
}

function cachedBinary() {
  const suffix = os.platform() === 'win32' ? '.exe' : ''
  return path.join(toolboxRoot(), 'bin', `toolbox-${releaseTag()}${suffix}`)
}

/** A binary staged next to this package (local build). */
function localBinary() {
  const override = process.env.TOOLBOX_BIN
  if (override && fs.existsSync(override)) return override
  const dir = path.join(HERE, 'bin')
  try {
    const staged = fs.readdirSync(dir)
      .filter((name) => /^toolbox.*\.exe$/i.test(name) || /^toolbox[^.]*$/i.test(name))
      .map((name) => path.join(dir, name))
      .sort((a, b) => fs.statSync(b).mtimeMs - fs.statSync(a).mtimeMs)
    if (staged.length > 0) return staged[0]
  } catch { /* no local build */ }
  return undefined
}

/** Resolve the binary without downloading anything (used for the config hint). */
function resolvedBinary() {
  return localBinary() || cachedBinary()
}

function extractZip(zip, dir) {
  if (process.platform === 'win32') {
    const result = spawnSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command',
      `Expand-Archive -LiteralPath '${zip}' -DestinationPath '${dir}' -Force`], { stdio: 'ignore' })
    if (result.status !== 0) throw new Error('解压失败：Expand-Archive 返回 ' + result.status)
    return
  }
  const result = spawnSync('unzip', ['-o', zip, '-d', dir], { stdio: 'ignore' })
  if (result.status !== 0) throw new Error('解压失败：unzip 返回 ' + result.status)
}

async function fetchBytes(url) {
  const response = await fetch(url, { redirect: 'follow' })
  if (!response.ok) throw new Error(`下载失败 HTTP ${response.status}: ${url}`)
  return Buffer.from(await response.arrayBuffer())
}

/** Download the release asset for this platform once, verified against SHA256SUMS. */
async function ensureBinary(log) {
  const ready = resolvedBinary()
  if (ready) return ready
  const asset = assetName()
  const base = `https://github.com/${REPO}/releases/download/${releaseTag()}`
  log(`toolbox binary missing, downloading ${asset} from ${releaseTag()}`)

  const zip = path.join(os.tmpdir(), `toolbox-${process.pid}-${Date.now()}.zip`)
  const bytes = await fetchBytes(`${base}/${asset}`)
  fs.writeFileSync(zip, bytes)

  try {
    const sums = (await fetchBytes(`${base}/SHA256SUMS`)).toString('utf8')
    const line = sums.split(/\r?\n/).map((row) => row.trim().split(/\s+/)).find((row) => row[1] === asset)
    if (!line) throw new Error(`SHA256SUMS 里没有 ${asset}`)
    const digest = crypto.createHash('sha256').update(bytes).digest('hex')
    if (digest !== line[0]) throw new Error(`sha256 校验失败（期望 ${line[0]}，实际 ${digest}）`)

    const stage = fs.mkdtempSync(path.join(os.tmpdir(), 'toolbox-x-'))
    extractZip(zip, stage)
    const name = process.platform === 'win32' ? 'toolbox.exe' : 'toolbox'
    const built = path.join(stage, name)
    if (!fs.existsSync(built)) throw new Error(`压缩包里没有 ${name}`)
    const target = cachedBinary()
    fs.mkdirSync(path.dirname(target), { recursive: true })
    fs.copyFileSync(built, target)
    if (process.platform !== 'win32') fs.chmodSync(target, 0o755)
    fs.rmSync(stage, { recursive: true, force: true })
    log(`toolbox binary cached at ${target}`)
    return target
  } catch (error) {
    throw new Error(`${error.message}；也可以自己构建后设置 TOOLBOX_BIN 指向它`)
  } finally {
    fs.rmSync(zip, { force: true })
  }
}

async function healthy(port) {
  try {
    const response = await fetch(`http://127.0.0.1:${port}/api/state`, { signal: AbortSignal.timeout(1500) })
    return response.ok
  } catch {
    return false
  }
}

/** Start (or reuse) the loopback API server and return its base URL. */
async function ensureServer(log) {
  if (serverPort && await healthy(serverPort)) return `http://127.0.0.1:${serverPort}`
  if (starting) return starting
  starting = (async () => {
    const binary = await ensureBinary(log)
    const port = 8790 + Math.floor(Math.random() * 40)
    const child = spawn(binary, ['-http', `127.0.0.1:${port}`, '-root', toolboxRoot()], {
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
    })
    serverChild = child
    child.stdout?.on('data', (chunk) => log(`toolbox api: ${String(chunk).trim()}`))
    child.stderr?.on('data', (chunk) => log(`toolbox api: ${String(chunk).trim()}`))
    child.on('exit', (code) => {
      if (serverChild === child) {
        serverChild = null
        serverPort = 0
      }
      log(`toolbox api exited (${code})`)
    })
    for (let attempt = 0; attempt < 40; attempt++) {
      if (await healthy(port)) {
        serverPort = port
        return `http://127.0.0.1:${port}`
      }
      if (child.exitCode !== null) break
      await new Promise((resolve) => setTimeout(resolve, 150))
    }
    try { child.kill() } catch { /* already gone */ }
    serverChild = null
    throw new Error(`toolbox api did not start on 127.0.0.1:${port}`)
  })()
  try {
    return await starting
  } finally {
    starting = null
  }
}

function sendJson(res, status, value) {
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' })
  res.end(JSON.stringify(value))
}

function isLoopback(address) {
  return address === '127.0.0.1' || address === '::1' || address === '::ffff:127.0.0.1'
}

/**
 * Same fence the framework's own panels use (loopback socket + browser marker).
 *
 * The desktop shell serves the Web GUI from `dsh-app://app/` and forwards the
 * page's Host requests itself, deleting `origin` and `sec-fetch-site` on the
 * way — a marker-only fence answered 403 to this plugin's own page. The shell
 * does attach the Host's browser-auth cookie, which is the first-party marker
 * of an application on this machine.
 */
function trustedRequest(req) {
  if (!isLoopback(req.socket?.remoteAddress)) return false
  if (req.headers['sec-fetch-site'] === 'same-origin') return true
  const origin = req.headers.origin
  if (typeof origin === 'string') {
    try {
      if (new URL(origin).host === req.headers.host) return true
    } catch { /* fall through to the cookie marker */ }
  }
  const cookie = req.headers.cookie
  if (typeof cookie === 'string') {
    return cookie.split(';').some((segment) => segment.trim().startsWith('dsh-auth-'))
  }
  return false
}

async function readBody(req, limit = 512 * 1024 * 1024) {
  const chunks = []
  let size = 0
  for await (const chunk of req) {
    size += chunk.length
    if (size > limit) throw new Error('body-too-large')
    chunks.push(chunk)
  }
  return Buffer.concat(chunks)
}

/** The exact MCP row a user pastes into their host config. */
function mcpConfig() {
  const command = resolvedBinary() || `<running toolbox binary>`
  return {
    command,
    root: toolboxRoot(),
    args: ['-mcp', '-root', toolboxRoot()],
    cordis: {
      insert: [
        { id: 'dsh-toolbox-mcp', name: '@deepseek-ai/dsh-mcp-client', config: {
          transport: 'stdio', serverName: 'toolbox', command,
          args: ['-mcp', '-root', toolboxRoot()], toolCallTimeoutMs: 600000, failOnStartupError: true,
        } },
      ],
    },
    json: { mcpServers: { toolbox: { command, args: ['-mcp', '-root', toolboxRoot()] } } },
  }
}

async function proxy(req, res, log) {
  if (!trustedRequest(req)) return sendJson(res, 403, { error: 'forbidden' })
  const raw = req.url || '/'
  const wantsLauncher = raw === `${API_PREFIX}/launcher`
  let base
  try {
    // Starting the server also resolves/downloads the binary, so the launcher
    // hint below can name the real path the user's MCP client will run.
    base = await ensureServer(log)
  } catch (error) {
    if (wantsLauncher) return sendJson(res, 200, mcpConfig())
    return sendJson(res, 503, { error: String(error?.message ?? error) })
  }
  if (wantsLauncher) return sendJson(res, 200, mcpConfig())
  const suffix = raw.replace(/^\/dsh-toolbox\/api\/v1/, '/api')
  const method = (req.method || 'GET').toUpperCase()
  try {
    const init = { method, headers: {} }
    if (method !== 'GET' && method !== 'HEAD') {
      init.body = await readBody(req)
      const contentType = req.headers['content-type']
      if (contentType) init.headers['content-type'] = contentType
    }
    const response = await fetch(base + suffix, init)
    const buffer = Buffer.from(await response.arrayBuffer())
    res.writeHead(response.status, {
      'content-type': response.headers.get('content-type') || 'application/json; charset=utf-8',
      'cache-control': 'no-store',
    })
    res.end(buffer)
  } catch (error) {
    sendJson(res, 502, { error: String(error?.message ?? error) })
  }
}

/**
 * @param ctx - Host plugin context.
 */
export function apply(ctx) {
  const log = (message) => {
    try { ctx.logger?.info?.(message) } catch { /* logging is best effort */ }
  }
  const register = () => ctx.webServer.register({
    kind: 'prefix',
    path: API_PREFIX,
    handler: (req, res) => proxy(req, res, log),
  })
  // ctx.effect owns the disposer: returning one from apply() does NOT unload the
  // route, and a bare register made the next mount fail with "duplicate prefix
  // route".
  if (typeof ctx.effect === 'function') ctx.effect(register)
  else register()

  return () => {
    if (serverChild) {
      try { serverChild.kill() } catch { /* already gone */ }
      serverChild = null
      serverPort = 0
    }
  }
}
