/* Theme bootstrap: runs before first paint to avoid a theme flash. */
(function () {
  var choice = 'dark';
  try { choice = localStorage.getItem('cr-theme') || 'dark'; } catch (e) {}
  var root = document.documentElement;
  var lightQuery = window.matchMedia('(prefers-color-scheme: light)');
  function resolved() {
    if (choice === 'system') return lightQuery.matches ? 'light' : 'dark';
    return choice === 'light' ? 'light' : 'dark';
  }
  function apply() {
    root.setAttribute('data-theme', resolved());
    root.setAttribute('data-theme-choice', choice);
  }
  apply();
  if (lightQuery.addEventListener) lightQuery.addEventListener('change', function () { if (choice === 'system') apply(); });
  window.__setTheme = function (next) {
    choice = next;
    try { localStorage.setItem('cr-theme', next); } catch (e) {}
    apply();
    document.dispatchEvent(new CustomEvent('cr-theme', { detail: next }));
  };
})();
