// Capture the console screenshots used in the README.
//
// This deliberately runs against the throwaway demo instance started by
// demo_console.ps1 (port 4799), never against a production gateway: the demo
// holds fabricated accounts only, so no real email, API key, proxy or
// conversation can end up in a published image.
//
// Usage: node capture_screenshots.js <demoAdminPassword>

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = 'http://127.0.0.1:4799';
const PASSWORD = process.argv[2];
const OUT = path.join(__dirname, '..', '..', '..', 'docs', 'screenshots');

if (BASE.includes('4141')) {
  throw new Error('refusing to screenshot a production port');
}

// The console switches views with internal state, so nav items are clicked by
// position. The order matches the NAV array in webapp/src/App.tsx.
const SHOTS = [
  ['03-usage.png', 1],
  ['04-accounts.png', 2],
  ['05-apikeys.png', 3],
  ['06-conversations.png', 4],
  ['07-proxies.png', 5],
  ['08-settings.png', 6],
];

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
  const browser = await chromium.launch({
    executablePath: fs.existsSync(CHROME) ? CHROME : undefined,
  });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();

  const res = await page.request.post(`${BASE}/api/admin/login`, {
    data: { password: PASSWORD },
  });
  console.log('login status:', res.status());
  if (!res.ok()) {
    console.log('body:', (await res.text()).slice(0, 200));
    await browser.close();
    process.exit(1);
  }

  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await sleep(1200);
  await page.screenshot({ path: path.join(OUT, '01-login.png') });
  console.log('wrote 01-login.png');

  await page.goto(`${BASE}/webapp/`, { waitUntil: 'networkidle' });
  await sleep(1800);
  await page.screenshot({ path: path.join(OUT, '02-dashboard.png') });
  console.log('wrote 02-dashboard.png');

  for (const [file, index] of SHOTS) {
    await page.locator('button').nth(index).click();
    await sleep(1500);
    await page.screenshot({ path: path.join(OUT, file) });
    console.log('wrote', file);
  }

  await browser.close();
})().catch((e) => {
  console.error(e.message);
  process.exit(1);
});