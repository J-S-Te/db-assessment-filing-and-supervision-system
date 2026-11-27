#!/usr/bin/env node
/**
 * 用 CDP 驱动一个 Chrome 实例逐帧截图（确定性时间轴，不依赖真实时钟）
 * 用法: node capture.mjs <mode> <durationMs> <fps> <outDir>
 */
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const PORT = 9300 + (process.pid % 300);
const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const PAGE = resolve(ROOT, 'index.html');
const [, , mode = 'loop', durationArg = '8000', fpsArg = '20', outArg = 'out/frames'] = process.argv;
const duration = +durationArg;
const fps = +fpsArg;
const outDir = resolve(ROOT, outArg);
const profile = `/tmp/chrome-cdp-${process.pid}-${Date.now()}`;

mkdirSync(outDir, { recursive: true });

const chrome = spawn(CHROME, [
  '--headless=new',
  '--disable-gpu',
  '--use-gl=swiftshader',
  '--enable-unsafe-swiftshader',
  '--hide-scrollbars',
  '--force-device-scale-factor=1',
  '--window-size=1920,1080',
  `--remote-debugging-port=${PORT}`,
  `--user-data-dir=${profile}`,
  'about:blank',
], { stdio: 'ignore' });

const sleep = ms => new Promise(r => setTimeout(r, ms));

async function waitPort() {
  for (let i = 0; i < 100; i++) {
    try {
      const r = await fetch(`http://127.0.0.1:${PORT}/json/version`);
      if (r.ok) return await r.json();
    } catch {}
    await sleep(100);
  }
  throw new Error('Chrome 调试端口未就绪');
}

class CDP {
  constructor(ws) { this.ws = ws; this.id = 0; this.pending = new Map(); this.handlers = new Map();
    ws.addEventListener('message', ev => {
      const msg = JSON.parse(ev.data);
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve: res, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        msg.error ? reject(new Error(JSON.stringify(msg.error))) : res(msg.result);
      } else if (msg.method && this.handlers.has(msg.method)) {
        this.handlers.get(msg.method).forEach(fn => fn(msg.params));
      }
    });
  }
  on(method, fn) { if (!this.handlers.has(method)) this.handlers.set(method, []); this.handlers.get(method).push(fn); }
  send(method, params = {}) {
    const id = ++this.id;
    return new Promise((res, rej) => {
      this.pending.set(id, { resolve: res, reject: rej });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }
}

try {
  await waitPort();
  const target = await (await fetch(`http://127.0.0.1:${PORT}/json/new?about:blank`, { method: 'PUT' })).json();
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise(r => ws.addEventListener('open', r));
  const cdp = new CDP(ws);

  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  await cdp.send('Emulation.setDeviceMetricsOverride', {
    width: 1920, height: 1080, deviceScaleFactor: 1, mobile: false,
  });
  const loaded = new Promise(res => cdp.on('Page.loadEventFired', res));
  await cdp.send('Page.navigate', { url: `file://${PAGE}?mode=${mode}&manual=1` });
  await loaded;

  // 等待资源就绪
  for (let i = 0; i < 120; i++) {
    const r = await cdp.send('Runtime.evaluate', {
      expression: `(document.body && document.body.dataset.ready==='1' && [...document.images].every(i=>i.complete)) ? 1 : 0`,
      returnByValue: true,
    });
    if (r.result.value === 1) break;
    await sleep(100);
  }
  await sleep(600);

  const total = Math.round(duration / 1000 * fps);
  for (let f = 0; f < total; f++) {
    const ms = Math.round(f * 1000 / fps);
    await cdp.send('Runtime.evaluate', { expression: `window.__setFrame(${ms})`, returnByValue: true });
    await cdp.send('Runtime.evaluate', {
      expression: `new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))`,
      awaitPromise: true, returnByValue: true,
    });
    const shot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
    writeFileSync(resolve(outDir, `f${String(f).padStart(4, '0')}.png`), Buffer.from(shot.data, 'base64'));
    if (f % 20 === 0) process.stdout.write(`  frame ${f}/${total}\r`);
  }
  console.log(`\n完成 ${total} 帧 -> ${outDir}`);
  ws.close();
} catch (err) {
  console.error('捕获失败:', err.message);
  process.exitCode = 1;
} finally {
  chrome.kill('SIGKILL');
  try { rmSync(profile, { recursive: true, force: true, maxRetries: 3 }); } catch {}
}
