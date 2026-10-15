// Preserve existing bookmarks while displaying the homepage directory URL.
if (location.pathname.endsWith('/index.html')) {
  history.replaceState(history.state, '', location.pathname.slice(0, -10) + location.search + location.hash);
}
// Keep screenshots and navigation usable without any external dependencies.
const tabs = [...document.querySelectorAll('[role="tab"]')];
function selectTab(tab) {
  for (const candidate of tabs) {
    const selected = candidate === tab;
    candidate.setAttribute('aria-selected', String(selected));
    candidate.tabIndex = selected ? 0 : -1;
    document.getElementById(candidate.getAttribute('aria-controls')).hidden = !selected;
  }
}
for (const tab of tabs) {
  tab.addEventListener('click', () => selectTab(tab));
  tab.addEventListener('keydown', event => {
    let index = tabs.indexOf(tab);
    if (event.key === 'ArrowRight') index = (index + 1) % tabs.length;
    else if (event.key === 'ArrowLeft') index = (index + tabs.length - 1) % tabs.length;
    else if (event.key === 'Home') index = 0;
    else if (event.key === 'End') index = tabs.length - 1;
    else return;
    event.preventDefault();
    selectTab(tabs[index]);
    tabs[index].focus();
  });
}
const dialog = document.getElementById('lightbox');
for (const button of document.querySelectorAll('[data-image]')) {
  button.addEventListener('click', () => {
    const img = dialog.querySelector('img');
    img.src = button.dataset.image;
    img.alt = button.querySelector('img').alt;
    dialog.querySelector('p').textContent = button.dataset.caption;
    dialog.showModal();
  });
}
dialog.querySelector('.close').addEventListener('click', () => dialog.close());
dialog.addEventListener('click', event => {
  // The dialog itself is the target when a click lands on its backdrop.
  if (event.target === dialog) {
    const bounds = dialog.getBoundingClientRect();
    if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) dialog.close();
  }
});
for (const button of document.querySelectorAll('[data-copy]')) {
  button.addEventListener('click', async () => {
    const content = document.getElementById(button.dataset.copy).textContent;
    try {
      await navigator.clipboard.writeText(content);
      button.textContent = 'Copied!';
    } catch {
      // Selection gives older browsers a useful fallback when clipboard access fails.
      const range = document.createRange();
      range.selectNodeContents(document.getElementById(button.dataset.copy));
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
      button.textContent = 'Selected — copy';
    }
  });
}

// Highlight the reader's current section without changing scroll position or history.
const sectionLinks = [...document.querySelectorAll('aside nav a[href^="#"]')];
if ('IntersectionObserver' in window && sectionLinks.length) {
  const observer = new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      for (const link of sectionLinks) {
        if (link.hash === `#${entry.target.id}`) link.setAttribute('aria-current', 'location');
        else link.removeAttribute('aria-current');
      }
    }
  }, { rootMargin: '-10% 0px -60% 0px' });
  for (const link of sectionLinks) {
    const section = document.getElementById(link.hash.slice(1));
    if (section) observer.observe(section);
  }
}
