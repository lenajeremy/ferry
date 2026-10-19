// Ferry's site: works out which system the visitor is on, so every "Download" button offers the right
// file, and fills in the downloads page. Every file comes from one place, the GitHub release.
const VERSION = '1.0.1';
const RELEASE = `https://github.com/lenajeremy/ferry/releases/download/v${VERSION}/`;

const PLATFORMS = {
  mac: {
    tab: 'Mac', title: 'Ferry for Mac', button: 'Download for Mac', picked: 'Picked for your Mac',
    req: 'macOS 11 or later, on Apple silicon or Intel.',
    file: 'Ferry-mac.dmg', size: '11.8 MB',
    others: [],
    brew: { cmd: 'brew install --cask lenajeremy/ferry/ferry-app', text: 'Or install it with Homebrew:' },
    steps: [
      ['Open <b>Ferry-mac.dmg</b>', ' and drag Ferry into Applications.'],
      ['Open Ferry.', ' The first time, macOS may say it can’t check the app: open System Settings › Privacy & Security and choose Open Anyway.'],
      ['Scan the code', ' on screen with your phone. That’s it.'],
    ],
  },
  windows: {
    tab: 'Windows', title: 'Ferry for Windows', button: 'Download for Windows', picked: 'Picked for your PC',
    req: 'Windows 10 or 11, 64-bit.',
    file: 'Ferry-windows-x64.exe', size: '13.2 MB',
    others: [{ label: 'For ARM PCs', file: 'Ferry-windows-arm64.exe' }],
    steps: [
      ['Open <b>Ferry-windows-x64.exe</b>.', ' There’s nothing to install.'],
      ['Let it through.', ' If Windows says it protected your PC, choose More info › Run anyway. When the firewall asks, allow private networks.'],
      ['Scan the code', ' on screen with your phone. That’s it.'],
    ],
  },
  linux: {
    tab: 'Linux', title: 'Ferry for Linux', button: 'Download for Linux', picked: 'Picked for your Linux computer',
    req: 'Ubuntu 22.04 or later, Debian 12 or later and others like them, 64-bit.',
    file: 'Ferry-linux-x64.deb', size: '4.7 MB',
    others: [
      { label: '.tar.gz', file: 'Ferry-linux-x64.tar.gz' },
      { label: 'ARM .deb', file: 'Ferry-linux-arm64.deb' },
      { label: 'ARM .tar.gz', file: 'Ferry-linux-arm64.tar.gz' },
    ],
    brew: { cmd: 'brew install lenajeremy/ferry/ferry && brew services start ferry', text: 'Or use Homebrew. Ferry then runs in the background and you use it in your browser:' },
    steps: [
      ['Install it:', ' open the file, or run <code>sudo apt install ./Ferry-linux-x64.deb</code>.'],
      ['Open Ferry', ' from your apps.'],
      ['Scan the code', ' on screen with your phone. That’s it.'],
    ],
  },
  ios: {
    tab: 'iPhone', title: 'Ferry for iPhone', button: 'Get the beta on TestFlight', picked: 'Picked for your iPhone',
    req: 'iOS 26 or later. The app is in beta, through Apple’s TestFlight.',
    url: 'https://testflight.apple.com/join/cxtxBs4E',
    note: 'Or skip the app: scan the code with the Camera and use Ferry in Safari.',
    others: [],
    steps: [
      ['Get Ferry on your computer', ' first: Mac, Windows or Linux.'],
      ['Open the TestFlight link on your iPhone.', ' Install TestFlight if it asks, then install Ferry from it.'],
      ['Scan the code', ' on your computer’s screen with the app. That’s it.'],
    ],
  },
  android: {
    tab: 'Android', title: 'Ferry for Android', button: 'Download for Android', picked: 'Picked for your Android phone',
    req: 'Android 10 or later.',
    file: 'Ferry-android.apk', size: '4.4 MB',
    others: [],
    steps: [
      ['Open <b>Ferry-android.apk</b>.', ' Your phone may ask you to allow installs from your browser first.'],
      ['Get Ferry on your computer', ' too: Mac, Windows or Linux.'],
      ['Scan the code', ' on your computer’s screen with the app.'],
    ],
  },
};

function detectPlatform() {
  const ua = navigator.userAgent;
  const platform = navigator.userAgentData?.platform || navigator.platform || '';
  if (/iPhone|iPad|iPod/.test(ua) || (platform === 'MacIntel' && navigator.maxTouchPoints > 1)) return 'ios';
  if (/Android/i.test(ua)) return 'android';
  if (/Win/i.test(platform) || /Windows/.test(ua)) return 'windows';
  if (/Mac/i.test(platform) || /Mac OS X/.test(ua)) return 'mac';
  if (/Linux|X11|CrOS/i.test(platform + ' ' + ua)) return 'linux';
  return null;
}

const detected = detectPlatform();

// Every "Download" button on the home page names the visitor's system and opens its downloads.
if (detected) {
  document.querySelectorAll('[data-download-cta], [data-download-link]').forEach(a => { a.href = `download.html#${detected}`; });
  document.querySelectorAll('[data-download-cta] span').forEach(span => { span.textContent = PLATFORMS[detected].button || 'Get Ferry'; });
}

// Copy buttons next to commands.
document.addEventListener('click', async e => {
  const button = e.target.closest('[data-copy]');
  if (!button) return;
  const code = button.parentElement.querySelector('code');
  try {
    await navigator.clipboard.writeText(code.textContent.trim());
    button.textContent = 'Copied';
  } catch {
    const range = document.createRange();
    range.selectNodeContents(code);
    getSelection().removeAllRanges();
    getSelection().addRange(range);
    button.textContent = 'Selected';
  }
  setTimeout(() => { button.textContent = 'Copy'; }, 1600);
});

// The downloads page.
const panel = document.getElementById('platform');
if (panel) {
  const tabs = document.getElementById('tabs');
  const icons = {
    down: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 4v12M6 11l6 6 6-6M5 20h14"/></svg>',
    compass: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="m15.5 8.5-2 5-5 2 2-5z"/></svg>',
  };

  const show = (key, focus) => {
    const p = PLATFORMS[key];
    tabs.querySelectorAll('button').forEach(b => {
      const on = b.dataset.p === key;
      b.setAttribute('aria-selected', on);
      b.tabIndex = on ? 0 : -1;
      if (on && focus) b.focus();
    });
    document.getElementById('picked').textContent = key === detected ? p.picked : '';

    const get = `<a class="btn primary big" href="${p.url || RELEASE + p.file}">${icons.down}<span>${p.button}</span></a>`
      + (p.size ? `<span class="size">${p.size}</span>` : '');
    const note = p.note ? `<p class="note">${icons.compass}<span>${p.note}</span></p>` : '';
    const others = p.others.length
      ? `<div class="others"><span>Also:</span>${p.others.map(o => `<a href="${RELEASE}${o.file}">${o.label}</a>`).join('')}</div>`
      : '';
    const brew = p.brew
      ? `<div class="brewbox"><p>${p.brew.text}</p><div class="cmd"><code>${p.brew.cmd}</code><button type="button" data-copy>Copy</button></div></div>`
      : '';
    panel.innerHTML = `
      <div class="main">
        <h2>${p.title}</h2>
        <p class="req">${p.req}</p>
        <div class="get">${get}</div>
        ${note}
        ${others}
        ${brew}
      </div>
      <div class="after">
        <h3>${p.url ? 'Get started' : 'After you download'}</h3>
        <ol>${p.steps.map(([lead, rest]) => `<li><span><b>${lead}</b>${rest}</span></li>`).join('')}</ol>
      </div>`;
    panel.setAttribute('aria-labelledby', `tab-${key}`);
    document.getElementById('next-phone').hidden = key === 'ios' || key === 'android';
    document.getElementById('next-computer').hidden = !(key === 'ios' || key === 'android');
  };

  tabs.addEventListener('click', e => {
    const b = e.target.closest('button');
    if (!b) return;
    show(b.dataset.p);
    history.replaceState(null, '', `#${b.dataset.p}`);
  });
  tabs.addEventListener('keydown', e => {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
    const keys = [...tabs.querySelectorAll('button')].map(b => b.dataset.p);
    const now = keys.indexOf(tabs.querySelector('[aria-selected="true"]').dataset.p);
    const next = keys[(now + (e.key === 'ArrowRight' ? 1 : keys.length - 1)) % keys.length];
    show(next, true);
    history.replaceState(null, '', `#${next}`);
  });
  document.querySelectorAll('[data-show]').forEach(a => a.addEventListener('click', e => {
    e.preventDefault();
    show(a.dataset.show);
    history.replaceState(null, '', `#${a.dataset.show}`);
    tabs.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }));

  // Release links in the table, and the version they're from.
  document.querySelectorAll('[data-file]').forEach(a => { a.href = RELEASE + a.dataset.file; });
  document.querySelectorAll('[data-version]').forEach(e => { e.textContent = VERSION; });

  const fromHash = location.hash.slice(1);
  show(PLATFORMS[fromHash] ? fromHash : detected || 'mac');
}
