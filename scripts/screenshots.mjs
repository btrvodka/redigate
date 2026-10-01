// Screenshots of the web UI for README and docs/web-ui.md.
//
//   make screenshots
//
// Expects redigate on REDIGATE_URL (default http://localhost:8080, `make up` starts a cluster with it),
// fills it with demo data through the HTTP API and drives headless Chrome over the DevTools protocol.
// Every page is captured in the light and the dark theme into docs/images/.
// Requires Node 22+ and Google Chrome or Chromium (CHROME overrides the binary).

import { spawn } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const base = process.env.REDIGATE_URL ?? 'http://localhost:8080';
const out = process.env.OUT ?? 'docs/images';
const width = 1280;
const height = 720;
const scale = Number(process.env.SCALE ?? 2);

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// --- demo data ---

// Deterministic pseudo-random numbers: the same data on every run.
let seed = 42;
const random = () => {
  seed = (seed * 1103515245 + 12345) % 2 ** 31;
  return seed / 2 ** 31;
};
const pick = (items) => items[Math.floor(random() * items.length)];
const quote = (value) => JSON.stringify(String(value));

const names = ['Alice', 'Bob', 'Carol', 'Dave', 'Erin', 'Frank', 'Grace', 'Heidi', 'Ivan', 'Judy', 'Mallory', 'Niaj', 'Olivia', 'Peggy', 'Rupert', 'Sybil', 'Trent', 'Victor', 'Walter', 'Yuki'];
const countries = ['DE', 'FR', 'NL', 'PL', 'ES', 'SE', 'US', 'JP', 'BR', 'CA'];
const plans = ['free', 'pro', 'team'];
const products = ['keyboard', 'mouse', 'monitor', 'headphones', 'webcam', 'dock', 'cable', 'laptop stand'];

function demoCommands() {
  const commands = [];
  const users = 60;

  for (let i = 1; i <= users; i++) {
    const name = pick(names);
    commands.push(`HSET user:${i} name ${name} email ${name.toLowerCase()}${i}@example.com country ${pick(countries)} plan ${pick(plans)} signed_up 2026-0${1 + Math.floor(random() * 9)}-1${Math.floor(random() * 9)}`);
    if (random() < 0.5) {
      commands.push(`RPUSH cart:${i} ${Array.from({ length: 1 + Math.floor(random() * 4) }, () => quote(pick(products))).join(' ')}`);
    }
  }

  for (let i = 0; i < 40; i++) {
    const id = Array.from({ length: 3 }, () => Math.floor(random() * 0x10000).toString(16).padStart(4, '0')).join('');
    const user = 1 + Math.floor(random() * users);
    commands.push(`SET session:${id} ${quote(JSON.stringify({ user, ip: `10.0.${Math.floor(random() * 255)}.${Math.floor(random() * 255)}` }))} EX ${600 + Math.floor(random() * 86400)}`);
  }

  for (let i = 1; i <= products.length; i++) {
    const product = { name: products[i - 1], price: Math.round(10 + random() * 400), stock: Math.floor(random() * 200), tags: [pick(['office', 'gaming', 'travel']), pick(['new', 'sale'])] };
    commands.push(`JSON.SET product:${i} $ ${quote(JSON.stringify(product))}`);
    commands.push(`SET stock:product:${i} ${product.stock}`);
  }

  for (let i = 0; i < 50; i++) {
    commands.push(`XADD orders * user ${1 + Math.floor(random() * users)} product ${quote(pick(products))} amount ${(5 + random() * 300).toFixed(2)} status ${pick(['paid', 'paid', 'shipped', 'pending'])}`);
  }

  commands.push(`ZADD leaderboard ${names.map((name) => `${Math.floor(random() * 5000)} ${name}`).join(' ')}`);
  commands.push(`SADD tags:popular office gaming travel sale new wireless`);
  commands.push(`HSET config:features dark_mode on checkout_v2 off search beta`);
  commands.push(`SET counter:page_views 18342`);
  commands.push(`LPUSH queue:emails welcome:12 reset:7 digest:31 welcome:44`);
  for (const country of countries) {
    commands.push(`PFADD visitors:${country} ${Array.from({ length: 20 }, (_, i) => `v${i}`).join(' ')}`);
  }

  return commands;
}

async function api(path, body) {
  const response = await fetch(base + path, { method: body ? 'POST' : 'GET', body });
  const json = await response.json();
  if (!response.ok) {
    throw new Error(`${path}: ${response.status} ${json.error_description}`);
  }
  return json.result;
}

async function seedData() {
  await api('/api/v1/command?target=masters', 'FLUSHALL');
  const { results } = await api('/api/v1/pipeline', demoCommands().join('\n'));
  const failed = results.filter((r) => r.error);
  for (const r of failed.slice(0, 5)) {
    console.warn('seed:', r.error);
  }
  console.log(`seed: ${results.length - failed.length} commands, ${failed.length} failed`);
}

// --- Chrome over the DevTools protocol ---

function findChrome() {
  const candidates = [
    process.env.CHROME,
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Chromium.app/Contents/MacOS/Chromium',
    '/usr/bin/google-chrome',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
  ];
  const chrome = candidates.find((path) => path && existsSync(path));
  if (!chrome) {
    throw new Error('Chrome is not found, set CHROME');
  }
  return chrome;
}

async function openBrowser() {
  const profile = mkdtempSync(join(tmpdir(), 'redigate-chrome-'));
  const port = 9300 + Math.floor(Math.random() * 500);
  const chrome = spawn(findChrome(), ['--headless=new', '--disable-gpu', '--hide-scrollbars', '--no-first-run',
    `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' });

  let target;
  for (let i = 0; i < 100 && !target; i++) {
    await sleep(100);
    try {
      const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      target = targets.find((t) => t.type === 'page');
    } catch {
      // Chrome is starting.
    }
  }
  if (!target) {
    chrome.kill();
    throw new Error('Chrome did not start');
  }

  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve);
    ws.addEventListener('error', reject);
  });

  let id = 0;
  const pending = new Map();
  ws.addEventListener('message', (event) => {
    const message = JSON.parse(event.data);
    if (message.id && pending.has(message.id)) {
      pending.get(message.id)(message);
      pending.delete(message.id);
    }
  });

  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const n = ++id;
    pending.set(n, (message) => (message.error ? reject(new Error(`${method}: ${message.error.message}`)) : resolve(message.result)));
    ws.send(JSON.stringify({ id: n, method, params }));
  });

  return {
    send,
    async close() {
      ws.close();
      const exited = new Promise((resolve) => chrome.once('exit', resolve));
      chrome.kill();
      await exited;
      rmSync(profile, { recursive: true, force: true, maxRetries: 5 });
    },
  };
}

async function main() {
  await seedData();

  const browser = await openBrowser();
  const { send } = browser;
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) {
      throw new Error(`${expression}: ${result.exceptionDetails.exception?.description}`);
    }
    return result.result.value;
  };
  // Waits until an expression is true: htmx loads fragments after the page.
  const until = async (expression) => {
    for (let i = 0; i < 100; i++) {
      if (await evaluate(expression)) {
        return;
      }
      await sleep(100);
    }
    throw new Error(`timeout: ${expression}`);
  };
  const go = async (path) => {
    await send('Page.navigate', { url: base + path });
    await until('document.readyState === "complete"');
  };

  try {
    await send('Page.enable');
    await send('Runtime.enable');
    await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: scale, mobile: false });

    // Swagger UI has no dark theme: one picture with the general group expanded. The long API
    // description is hidden to keep the operations in the frame.
    await go('/api/v1/docs/');
    await until('document.querySelectorAll(".opblock-tag").length > 10');
    await evaluate('document.querySelector(".info .description").style.display = "none"; document.querySelector(".opblock-tag[data-tag=general]").click()');
    await until('document.querySelectorAll(".opblock").length >= 6');
    await sleep(500);
    {
      const { data } = await send('Page.captureScreenshot', { format: 'png', clip: { x: 0, y: 0, width, height: 1000, scale: 1 }, captureBeyondViewport: true });
      writeFileSync(join(out, 'api-docs.png'), Buffer.from(data, 'base64'));
      console.log('saved', join(out, 'api-docs.png'));
    }

    for (const theme of ['light', 'dark']) {
      await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] });
      // Captures the page down to the footer (the whole page without one), at most maxHeight pixels.
      const shot = async (name, maxHeight = height) => {
        await sleep(300);
        const bottom = await evaluate('Math.ceil(document.querySelector("footer")?.getBoundingClientRect().bottom ?? document.documentElement.scrollHeight)');
        const clip = { x: 0, y: 0, width, height: Math.min(bottom, maxHeight), scale: 1 };
        const { data } = await send('Page.captureScreenshot', { format: 'png', clip, captureBeyondViewport: true });
        const file = join(out, `${name}-${theme}.png`);
        writeFileSync(file, Buffer.from(data, 'base64'));
        console.log('saved', file);
      };

      await go('/ui/');
      await shot('overview');

      await go('/ui/keys');
      await until('document.querySelectorAll("#rows tr[id]").length > 20');
      await evaluate('document.querySelector("#rows tr[id]").scrollIntoView(); [...document.querySelectorAll("#rows tr[id]")].find((r) => r.cells[0].textContent.trim() === "user:7").click()');
      await until('document.querySelector("#key-view h2")?.textContent.trim() === "user:7"');
      await shot('keys');

      await go('/ui/console');
      const run = async (command, target = 'auto') => {
        const before = await evaluate('document.querySelectorAll("#log .result").length');
        await evaluate(`{ const f = document.querySelector("form.console"); f.command.value = ${JSON.stringify(command)}; f.target.value = ${JSON.stringify(target)}; f.requestSubmit(); }`);
        await until(`document.querySelectorAll("#log .result").length > ${before}`);
      };
      await run('DBSIZE', 'masters');
      await run('XLEN orders');
      await run('HGET user:7 email');
      await evaluate('document.activeElement.blur()');
      await shot('console', 860);

    }
  } finally {
    await browser.close();
  }
}

await main();
