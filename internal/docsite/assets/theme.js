'use strict';
(() => {
  const key = 'facet-docs-theme';
  const system = window.matchMedia('(prefers-color-scheme: dark)');
  let preference;
  try { const saved = localStorage.getItem(key); if (saved === 'dark' || saved === 'light') preference = saved; } catch { /** Storage is optional. */ }
  const current = () => preference || (system.matches ? 'dark' : 'light');
  function apply() {
    const dark = current() === 'dark';
    document.documentElement.dataset.theme = dark ? 'dark' : 'light';
    const button = document.getElementById('theme-toggle');
    if (button) {
      const german = document.documentElement.lang === 'de';
      const label = dark ? (german ? 'Hellen Modus aktivieren' : 'Switch to light mode') : (german ? 'Dunklen Modus aktivieren' : 'Switch to dark mode');
      button.setAttribute('aria-label', german ? 'Dunkler Modus' : 'Dark mode');
      button.setAttribute('aria-pressed', String(dark));
      button.title = label;
      button.hidden = false;
    }
  }
  // Apply before the stylesheet is loaded to avoid flashing the wrong palette.
  apply();
  system.addEventListener('change', () => { if (!preference) apply(); });
  window.addEventListener('storage', event => {
    if (event.key === key || event.key === null) {
      preference = event.newValue === 'dark' || event.newValue === 'light' ? event.newValue : undefined;
      apply();
    }
  });
  document.addEventListener('DOMContentLoaded', () => {
    apply();
    document.getElementById('theme-toggle')?.addEventListener('click', () => {
      preference = current() === 'dark' ? 'light' : 'dark';
      try { localStorage.setItem(key, preference); } catch { /** Toggling still works without storage. */ }
      apply();
    });
  });
})();
