// Verifies that nothing sensitive can reach a README screenshot.
//
// The screenshot pass runs against a throwaway demo instance, so the real check
// is that the rendered DOM contains no production identifiers: the operator's
// mail domain, any real tenant id, or a full API key.
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = 'http://127.0.0.1:4799';
const PASSWORD = process.argv[2];

// Values that must never appear in a published screenshot.
const FORBIDDEN = [
  'zzzgfwacnz1',
  'office.mzz.edu.rs',
  'office.bo.edu.kg',
  'LNBuuAsbrS47XUM',
  'm365_05b9f7a',
  'm365_9b7a656',
  'm365_9d64558',
  'm365_6eb3851',
  'm365_924b307',
  'm365_7734f1a',
  'm365_9720c89',
  'm365_a94257a',
  'm365_c1f4417',
  'm365_fd95b9e',
  'm365_cfd4ce0',
  'm365_05deea2',
  'm365_406c730',
  'm365_8ef0257',
  'm365_5ae8f0b',
];

(async () => {
  const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
  const browser = await chromium.launch({
    executablePath: fs.existsSync(CHROME) ? CHROME : undefined,
  });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();

  const res = await page.request.post(`${BASE}/api/admin/login`, {
    data: { password: PASSWORD },
  });
  if (!res.ok()) {
    console.log('login failed', res.status());
    process.exit(1);
  }

  const pages = [
    '/login',
    '/webapp/',
    '/webapp/#usage',
    '/webapp/#accounts',
    '/webapp/#keys',
    '/webapp/#conversations',
    '/webapp/#settings',
  ];

  let bad = 0;
  for (const p of pages) {
    await page.goto(BASE + p, { waitUntil: 'networkidle' });
    await new Promise((r) => setTimeout(r, 1200));
    const text = await page.evaluate(() => document.body.innerText);
    for (const needle of FORBIDDEN) {
      if (text.includes(needle)) {
        console.log(`LEAK on ${p}: ${needle}`);
        bad++;
      }
    }
  }

  console.log(bad === 0 ? 'clean: no production identifiers in any view' : `found ${bad} leaks`);
  await browser.close();
  process.exit(bad === 0 ? 0 : 1);
})().catch((e) => {
  console.error(e.message);
  process.exit(1);
});