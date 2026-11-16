const links = [...document.querySelectorAll('nav a')];
const articles = [...document.querySelectorAll('main article')];
const input = document.querySelector('#search');
const normalize = value => value.normalize('NFC').toLocaleLowerCase('ru').replaceAll('ё', 'е');

function show() {
  let id;
  try { id = decodeURIComponent(location.hash.slice(1) || 'home'); }
  catch { id = 'home'; }
  if (id === 'main') return;
  const article = articles.find(item => id === item.id || id.startsWith(item.id + '-')) || articles[0];
  articles.forEach(item => { item.hidden = item !== article; });
  links.forEach(link => {
    if (link.dataset.id === article.id) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });
  document.title = article.dataset.title + ' · Бизнес документация Pulse';
  if (id !== article.id && id !== 'main') {
    requestAnimationFrame(() => document.getElementById(id)?.scrollIntoView());
  } else window.scrollTo(0, 0);
}

function search() {
  const query = normalize(input.value).trim();
  const matches = [];
  links.forEach(link => {
    const article = document.getElementById(link.dataset.id);
    const hit = !query || normalize(article.textContent).includes(query);
    link.hidden = !hit;
    if (hit) matches.push(link);
  });
  document.querySelector('#search-status').textContent = query
    ? 'Найдено разделов: ' + matches.length : 'Все разделы';
  document.querySelector('#no-results').hidden = matches.length > 0;
  const current = links.find(link => link.getAttribute('aria-current') === 'page');
  if (query && matches.length && current?.hidden) location.hash = matches[0].dataset.id;
}

window.addEventListener('hashchange', show);
input.addEventListener('input', search);
document.querySelector('#print').addEventListener('click', () => window.print());
show();
search();
