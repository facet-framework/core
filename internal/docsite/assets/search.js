'use strict';
(() => {
  const german = document.body.dataset.language === 'de';
  const select = document.querySelector('#perspective');
  const storageKey = `facet-docs-view:${document.body.dataset.site}`;
  const validViews = new Set([...select.options].map(option => option.value));
  let saved;
  try { saved = localStorage.getItem(storageKey); } catch { /** Storage is optional. */ }
  const requested = new URLSearchParams(location.search).get('view');
  select.value = validViews.has(requested) ? requested : validViews.has(saved) ? saved : (select.options[0]?.value || "");
  const withView = href => {
    const [base, fragment] = href.split('#');
    const separator = base.includes('?') ? '&' : '?';
    return `${base}${separator}view=${encodeURIComponent(select.value)}${fragment ? `#${fragment}` : ''}`;
  };
  function applyPerspective() {
    for (const note of document.querySelectorAll('[data-perspective]')) {
      note.hidden = note.dataset.perspective !== select.value;
    }
    for (const link of document.querySelectorAll('a[href]')) {
      const href = link.dataset.originalHref || link.getAttribute('href');
      if (!href || href.startsWith('#') || /^(https?:|mailto:)/.test(href) || !href.includes('.html')) continue;
      link.dataset.originalHref = href;
      link.href = withView(href);
    }
    try { localStorage.setItem(storageKey, select.value); } catch { /** Storage is optional. */ }
    try { const url = new URL(location.href); url.searchParams.set('view', select.value); history.replaceState(null, '', url); } catch { /** file: can restrict history. */ }
  }
  select.addEventListener('change', applyPerspective);
  applyPerspective();
  const dialog = document.querySelector('#doc-search');
  const queryInput = document.querySelector('#search-query');
  const results = document.querySelector('#search-results');
  const status = document.querySelector('#search-status');
  const normalize = text => String(text || '').toLocaleLowerCase().normalize('NFD').replace(/\p{M}/gu, '').replace(/ß/g, 'ss');
  const stopwords = new Set('der die das den dem des ein eine einen einer eines wie was welche welcher welches wer wo warum kann ich wir ist sind und oder mit von fur zu im in am auf funktioniert the a an and or with without how what which who where why can does do is are on at for of to it its by from this that'.split(' '));
  const tokens = text => (normalize(text).match(/[\p{L}\p{N}]+/gu) || []).filter(word => !stopwords.has(word));
  const index = (window.FACET_DOCS_SEARCH || []).map(entry => ({...entry, normalized: normalize(entry.text), normalizedTitle: normalize(entry.title), titleTokens: tokens(entry.title), normalizedDescription: normalize(entry.description)}));
  function renderSearch() {
    const words = tokens(queryInput.value);
    const matches = index.filter(entry => words.every(word => entry.normalized.includes(word))).map(entry => ({entry, score: words.reduce((score, word) => score + (entry.titleTokens.includes(word) ? 60 : entry.normalizedTitle.includes(word) ? 15 : 0) + (entry.normalizedDescription.includes(word) ? 8 : 0), entry.kind === 'article' ? 12 : 0)})).sort((a,b) => b.score-a.score || a.entry.title.localeCompare(b.entry.title));
    const displayed = words.length ? matches.slice(0,40) : matches.filter(match => match.entry.kind === 'article').slice(0,10);
    results.replaceChildren();
    status.textContent = words.length ? `${matches.length} ${german ? 'Treffer' : 'results'}` : (german ? 'Themen und häufige Fragen' : 'Topics and common questions');
    if (!displayed.length) { const empty = document.createElement('p'); empty.className = 'search-status'; empty.textContent = german ? 'Keine passenden Inhalte. Versuche einen anderen Suchbegriff.' : 'No matching content. Try another term.'; results.append(empty); }
    for (const {entry} of displayed) {
      const link = document.createElement('a'); link.className = 'search-result'; link.href = withView(entry.url);
      const title = document.createElement('strong'); title.textContent = entry.title;
      if (entry.lifecycle && entry.lifecycle !== 'active') {const badge=document.createElement('span');badge.className='status-badge';badge.textContent=({planned:german?'Geplant':'Planned',deprecated:german?'Abgekündigt':'Deprecated'})[entry.lifecycle] || (german?'Je nach Variante':'Depends on variant');title.append(' ',badge);}
      const category = document.createElement('small'); category.textContent = entry.category || (german ? 'Funktion' : 'Feature');
      const description = document.createElement('p'); description.textContent = entry.description;
      link.append(title,category,description); results.append(link);
    }
  }
  let opener;
  function openSearch(button) { opener = button; if (!dialog.open) dialog.showModal(); renderSearch(); queryInput.focus(); }
  document.querySelectorAll('[data-open-search]').forEach(button => button.addEventListener('click', () => openSearch(button)));
  document.querySelector('[data-close-search]').addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => opener?.focus());
  dialog.addEventListener('click', event => { if (event.target === dialog) { const r = dialog.getBoundingClientRect(); if (event.clientX < r.left || event.clientX > r.right || event.clientY < r.top || event.clientY > r.bottom) dialog.close(); } });
  queryInput.addEventListener('input', renderSearch);
  queryInput.addEventListener('keydown', event => { if (event.key === 'ArrowDown') { event.preventDefault(); results.querySelector('a')?.focus(); } if (event.key === 'Enter') { const first = results.querySelector('a'); if (first) {dialog.close();location.href = first.href;} } });
  results.addEventListener('click',event=>{if(event.target.closest('a'))dialog.close();});
  results.addEventListener('keydown', event => { const links = [...results.querySelectorAll('a')]; const current = links.indexOf(document.activeElement); if (event.key === 'ArrowDown') { event.preventDefault(); links[Math.min(current+1,links.length-1)]?.focus(); } if (event.key === 'ArrowUp') { event.preventDefault(); if (current <= 0) queryInput.focus(); else links[current-1]?.focus(); } });
  document.addEventListener('keydown', event => { if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); openSearch(document.querySelector('[data-open-search]')); } if (event.key === '/' && !/INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName)) { event.preventDefault(); openSearch(document.querySelector('[data-open-search]')); } });
  const menu = document.querySelector('#menu-toggle');
  menu.addEventListener('click', () => { const open = document.querySelector('#sidebar').classList.toggle('is-open'); menu.setAttribute('aria-expanded',String(open)); });
  const filter = document.querySelector('#feature-filter');
  if (filter) filter.addEventListener('input', () => { const words = tokens(filter.value); let visible=0; for (const row of document.querySelectorAll('[data-feature-search]')) { row.hidden = !words.every(word => normalize(row.dataset.featureSearch + ' ' + row.querySelector('.feature-details').textContent).includes(word)); if (!row.hidden) visible++; } for (const group of document.querySelectorAll('.catalog-group')) group.hidden = ![...group.querySelectorAll('[data-feature-search]')].some(row=>!row.hidden); document.querySelector('#feature-status').textContent = `${visible} ${german ? 'Funktionen' : 'features'}`; });
  function revealFragment() { let id; try { id = decodeURIComponent(location.hash.slice(1)); } catch { return; } if (!id) return; const target = document.getElementById(id); if (target?.tagName === 'DETAILS') target.open = true; }
  revealFragment(); window.addEventListener('hashchange', revealFragment);
  for (const button of document.querySelectorAll('.copy-code')) button.addEventListener('click', async () => { const code = button.parentElement.querySelector('code'); try { await navigator.clipboard.writeText(code.textContent); button.textContent = german ? 'Kopiert' : 'Copied'; } catch { const selection = window.getSelection(); const range = document.createRange(); range.selectNodeContents(code); selection.removeAllRanges(); selection.addRange(range); } });
  if ('IntersectionObserver' in window) { const observer = new IntersectionObserver(entries => { for (const entry of entries) if (entry.isIntersecting) { document.querySelectorAll('.on-this-page a').forEach(link=>link.classList.toggle('current',link.hash===`#${entry.target.id}`)); } },{rootMargin:'-10% 0px -65% 0px'}); document.querySelectorAll('.doc-section').forEach(section=>observer.observe(section)); }
})();
