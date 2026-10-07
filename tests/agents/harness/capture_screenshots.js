// Capture README screenshots from the static demo page.
//
// Nothing here talks to a running gateway: demo_ui/console.html is a single
// self-contained file with the shipped design tokens and fabricated values, so
// no account, key, proxy or conversation from a real deployment can appear.

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const ROOT = path.join(__dirname, '..', '..', '..');
const PAGE = 'file:///' + path.join(__dirname, 'demo_ui', 'console.html').replace(/\\/g, '/');
const OUT = path.join(ROOT, 'docs', 'screenshots');

const SHOTS = [
  ['01-login.png', 'login'],
  ['02-dashboard.png', 'dashboard'],
  ['03-usage.png', 'usage'],
  ['04-accounts.png', 'accounts'],
  ['05-apikeys.png', 'keys'],
  ['06-conversations.png', 'conversations'],
  ['07-proxies.png', 'proxies'],
  ['08-settings.png', 'settings'],
];

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
  const browser = await chromium.launch({
    executablePath: fs.existsSync(CHROME) ? CHROME : undefined,
  });

  for (const [file, view] of SHOTS) {
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
    await page.goto(`${PAGE}#${view}`, { waitUntil: 'load' });
    await page.waitForTimeout(350);
    await page.screenshot({ path: path.join(OUT, file) });
    await page.close();
    console.log('wrote', file);
  }

  await browser.close();
})().catch((e) => {
  console.error(e.message);
  process.exit(1);
});
