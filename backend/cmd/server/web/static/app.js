/* Call Recorder UI behaviour: shared audio player, theme, navigation,
   confirmation dialogs, admin edit fill. No framework dependencies. */
(function () {
  'use strict';

  function $(id) { return document.getElementById(id); }
  function on(el, ev, fn) { if (el) el.addEventListener(ev, fn); }

  /* ---------- Theme toggle ---------- */
  var themeBtn = $('theme-toggle');
  function paintTheme() {
    if (!themeBtn) return;
    var choice = document.documentElement.getAttribute('data-theme-choice') || 'dark';
    themeBtn.textContent = choice === 'dark' ? 'Dark' : choice === 'light' ? 'Light' : 'Auto';
    themeBtn.setAttribute('aria-label', 'Theme: ' + choice + '. Activate to change.');
  }
  on(themeBtn, 'click', function () {
    var choice = document.documentElement.getAttribute('data-theme-choice') || 'dark';
    window.__setTheme(choice === 'dark' ? 'light' : choice === 'light' ? 'system' : 'dark');
  });
  document.addEventListener('cr-theme', paintTheme);
  paintTheme();

  /* ---------- Mobile navigation ---------- */
  var navToggle = $('nav-toggle');
  var mainNav = $('main-nav');
  on(navToggle, 'click', function () {
    var open = mainNav.classList.toggle('open');
    navToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  });

  /* Collapse the filter panel by default on small screens. */
  var filters = document.querySelector('details.filters');
  if (filters && window.matchMedia('(max-width: 767px)').matches && !window.location.search) {
    filters.removeAttribute('open');
  }

  /* ---------- Backend status ---------- */
  var dot = $('status-dot');
  if (dot && window.fetch) {
    fetch('/healthz').then(function (r) { return r.ok ? r.json() : Promise.reject(); })
      .then(function (j) {
        dot.classList.add('ok');
        dot.setAttribute('aria-label', 'Backend healthy, version ' + (j.version || 'unknown'));
        dot.title = 'Backend healthy · ' + (j.version || '');
      })
      .catch(function () {
        dot.classList.add('down');
        dot.setAttribute('aria-label', 'Backend unavailable');
        dot.title = 'Backend unavailable';
      });
  }

  /* ---------- Confirmation dialogs ---------- */
  var dialog = document.createElement('dialog');
  dialog.className = 'confirm';
  dialog.innerHTML = '<p id="confirm-text"></p>' +
    '<div class="dialog-actions">' +
    '<button class="btn ghost" value="cancel">Cancel</button>' +
    '<button class="btn danger" value="confirm" id="confirm-yes">Confirm</button>' +
    '</div>';
  document.body.appendChild(dialog);
  var pendingForm = null;
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!(form instanceof HTMLFormElement)) return;
    var message = form.getAttribute('data-confirm');
    if (!message || form.dataset.confirmed === 'yes') return;
    e.preventDefault();
    pendingForm = form;
    $('confirm-text').textContent = message;
    dialog.showModal();
  }, true);
  dialog.addEventListener('close', function () {
    if (dialog.returnValue === 'confirm' && pendingForm) {
      pendingForm.dataset.confirmed = 'yes';
      if (pendingForm.requestSubmit) pendingForm.requestSubmit();
      else pendingForm.submit();
    }
    pendingForm = null;
  });

  /* ---------- Admin alias edit fill ---------- */
  document.addEventListener('click', function (e) {
    var btn = e.target.closest ? e.target.closest('[data-edit]') : null;
    if (!btn) return;
    var form = $(btn.getAttribute('data-edit'));
    if (!form) return;
    ['system', 'id', 'alias', 'description', 'category', 'priority'].forEach(function (name) {
      var field = form.elements[name];
      if (field && btn.dataset[name] !== undefined) field.value = btn.dataset[name];
    });
    var source = form.elements['source'];
    if (source && btn.dataset.source) source.value = btn.dataset.source === 'received' ? 'manual' : btn.dataset.source;
    var enabled = form.elements['enabled'];
    if (enabled) enabled.checked = btn.dataset.enabled === 'true';
    var heading = $('alias-form-heading');
    if (heading) heading.textContent = 'Edit alias ' + (btn.dataset.system || '') + ' / ' + (btn.dataset.id || '');
    var aliasField = form.elements['alias'];
    if (aliasField) aliasField.focus();
  });

  /* ---------- Shared audio player ---------- */
  var audio = $('player-audio');
  var liveStatus = $('live-status');
  var liveToggle = $('live-toggle');
  if (liveStatus && window.EventSource) {
    var updatesPaused = readLivePause();
    var queuedUpdates = 0;
    function readLivePause() { try { return sessionStorage.getItem('cr-live-paused') === 'true'; } catch (e) { return false; } }
    function refreshCalls() { if (window.htmx) window.htmx.ajax('GET', '/calls?' + window.location.search.replace(/^\?/, ''), {target: '#calls', swap: 'innerHTML'}); }
    function setPause(paused) {
      updatesPaused = paused;
      try { sessionStorage.setItem('cr-live-paused', paused ? 'true' : 'false'); } catch (e) {}
      if (liveToggle) { liveToggle.textContent = paused ? 'Resume updates' : 'Pause updates'; liveToggle.setAttribute('aria-pressed', paused ? 'true' : 'false'); }
      if (paused) { liveStatus.textContent = 'Live updates: paused'; liveStatus.className = 'live-status paused'; }
      else if (queuedUpdates) { liveStatus.textContent = 'Live updates: refreshing ' + queuedUpdates + ' new call' + (queuedUpdates === 1 ? '' : 's'); liveStatus.className = 'live-status live'; queuedUpdates = 0; refreshCalls(); }
    }
    if (liveToggle) liveToggle.addEventListener('click', function () { setPause(!updatesPaused); });
    setPause(updatesPaused);
    var live = new EventSource('/events/calls' + window.location.search);
    live.onopen = function () { if (!updatesPaused) { liveStatus.textContent = 'Live updates: connected'; liveStatus.className = 'live-status live'; } };
    live.addEventListener('calls', function () {
      queuedUpdates++;
      if (updatesPaused) { liveStatus.textContent = 'Live updates: paused (' + queuedUpdates + ' new call' + (queuedUpdates === 1 ? '' : 's') + ')'; liveStatus.className = 'live-status paused'; return; }
      var active = $('player-audio') && !$('player-audio').paused;
      if (active) { liveStatus.textContent = 'Live updates: ' + queuedUpdates + ' new call' + (queuedUpdates === 1 ? '' : 's') + ' available after playback'; liveStatus.className = 'live-status live'; return; }
      liveStatus.textContent = 'Live updates: updating'; liveStatus.className = 'live-status live'; queuedUpdates = 0; refreshCalls();
    });
    live.onerror = function () { if (!updatesPaused) { liveStatus.textContent = 'Live updates: reconnecting'; liveStatus.className = 'live-status reconnecting'; } };
    window.setInterval(function () { if (!updatesPaused && live.readyState !== 1 && !($('player-audio') && !$('player-audio').paused)) refreshCalls(); }, 30000);
  }
  if (!audio) return;
  var bar = $('player-bar');
  var btnPlay = $('pp-play'), btnPrev = $('pp-prev'), btnNext = $('pp-next'), btnStop = $('pp-stop');
  var titleEl = $('pp-title'), metaEl = $('pp-meta');
  var curEl = $('pp-cur'), durEl = $('pp-dur'), seek = $('pp-seek');
  var seqBox = $('pp-seq'), speedSel = $('pp-speed'), volRange = $('pp-vol');
  var announce = $('pp-announce');

  var queue = [];
  var index = -1;
  var playingId = null;

  function store(key, value) { try { sessionStorage.setItem(key, value); } catch (e) {} }
  function read(key) { try { return sessionStorage.getItem(key); } catch (e) { return null; } }
  function say(text) { if (announce) announce.textContent = text; }
  function fmt(sec) {
    if (!isFinite(sec) || sec < 0) return '0:00';
    var m = Math.floor(sec / 60), s = Math.floor(sec % 60);
    return m + ':' + (s < 10 ? '0' : '') + s;
  }

  if (read('cr-seq') === 'off' && seqBox) seqBox.checked = false;
  if (read('cr-speed') && speedSel) speedSel.value = read('cr-speed');
  if (read('cr-vol') && volRange) volRange.value = read('cr-vol');
  audio.volume = volRange ? parseFloat(volRange.value) : 1;

  function applyRate() { audio.playbackRate = parseFloat(speedSel ? speedSel.value : '1') || 1; }
  applyRate();

  function reindex() {
    var prior = null;
    if (playingId) {
      for (var p = 0; p < queue.length; p++) if (queue[p].id === playingId) { prior = queue[p]; break; }
    }
    queue = Array.prototype.map.call(document.querySelectorAll('[data-play]'), function (btn) {
      return { id: btn.getAttribute('data-play'), title: btn.getAttribute('data-title') || 'call', meta: btn.getAttribute('data-meta') || '', btn: btn };
    });
    if (playingId) {
      var still = -1;
      queue.forEach(function (item, i) { if (item.id === playingId) still = i; });
      // Keep the currently playing item in the in-memory queue if a refresh or
      // filter removes its row. This prevents SSE updates from interrupting it.
      if (still === -1 && prior) { queue.splice(Math.max(0, index), 0, prior); still = Math.max(0, index); }
      if (still !== -1) index = still;
    }
    paintButtons();
  }

  function icon(name) { return '<svg class="ic" aria-hidden="true"><use href="#i-' + name + '"/></svg>'; }

  function paintButtons() {
    queue.forEach(function (item) {
      var active = item.id === playingId && !audio.paused;
      var row = item.btn.closest('tr');
      item.btn.innerHTML = icon(active ? 'pause' : 'play');
      item.btn.setAttribute('aria-label', (active ? 'Pause ' : 'Play ') + item.title);
      if (row) row.classList.toggle('is-playing', item.id === playingId);
      else item.btn.classList.toggle('is-playing', item.id === playingId);
    });
    if (btnPlay) btnPlay.innerHTML = icon(playingId && !audio.paused ? 'pause' : 'play');
    if (btnPrev) btnPrev.disabled = index <= 0;
    if (btnNext) btnNext.disabled = index === -1 || index >= queue.length - 1;
  }

  function showBar() { bar.hidden = false; }

  function playAt(i, autoplay) {
    if (i < 0 || i >= queue.length) return;
    index = i;
    var item = queue[i];
    playingId = item.id;
    if (audio.getAttribute('src') !== '/media/' + item.id) {
      audio.setAttribute('src', '/media/' + item.id);
    }
    applyRate();
    titleEl.textContent = item.title;
    if (metaEl) metaEl.textContent = item.meta;
    showBar();
    var promise = audio.play();
    if (promise && promise.catch) promise.catch(function () { say('Playback could not start'); });
    say('Playing ' + item.title);
    paintButtons();
  }

  function toggleCurrent() {
    if (playingId === null) { playAt(index === -1 ? 0 : index); return; }
    if (audio.paused) { var p = audio.play(); if (p && p.catch) p.catch(function () {}); say('Playing ' + titleEl.textContent); }
    else { audio.pause(); say('Paused'); }
    paintButtons();
  }

  function stopPlayback() {
    audio.pause();
    playingId = null;
    index = -1;
    paintButtons();
  }

  function findById(id) {
    for (var i = 0; i < queue.length; i++) if (queue[i].id === id) return i;
    return -1;
  }

  document.addEventListener('click', function (e) {
    var btn = e.target.closest ? e.target.closest('[data-play]') : null;
    if (!btn) return;
    var id = btn.getAttribute('data-play');
    if (id === playingId && !audio.paused) { audio.pause(); say('Paused'); paintButtons(); return; }
    var i = findById(id);
    if (i !== -1) playAt(i, true);
  });

  on(btnPlay, 'click', toggleCurrent);
  on(btnPrev, 'click', function () { if (index > 0) playAt(index - 1, true); });
  on(btnNext, 'click', function () { if (index < queue.length - 1) playAt(index + 1, true); });
  on(btnStop, 'click', function () { stopPlayback(); bar.hidden = true; say('Stopped'); });

  on(seqBox, 'change', function () { store('cr-seq', seqBox.checked ? 'on' : 'off'); });
  on(speedSel, 'change', function () { applyRate(); store('cr-speed', speedSel.value); });
  on(volRange, 'input', function () { audio.volume = parseFloat(volRange.value); store('cr-vol', volRange.value); });

  audio.addEventListener('ended', function () {
    if (seqBox && seqBox.checked && index < queue.length - 1) { playAt(index + 1, true); return; }
    say('End of call list');
    paintButtons();
  });
  audio.addEventListener('play', paintButtons);
  audio.addEventListener('pause', paintButtons);
  audio.addEventListener('loadedmetadata', function () { if (durEl) durEl.textContent = fmt(audio.duration); });
  audio.addEventListener('timeupdate', function () {
    if (curEl) curEl.textContent = fmt(audio.currentTime);
    if (seek && isFinite(audio.duration) && audio.duration > 0 && document.activeElement !== seek) {
      seek.value = Math.round((audio.currentTime / audio.duration) * 1000);
    }
  });
  on(seek, 'input', function () {
    if (isFinite(audio.duration) && audio.duration > 0) {
      audio.currentTime = (seek.value / 1000) * audio.duration;
    }
  });

  /* Rebuild the queue after HTMX swaps in a new call list, and mirror the
     fragment URL onto the full page URL so filters stay shareable. */
  document.body.addEventListener('htmx:afterSwap', function (e) {
    if (!(e.detail && e.detail.target && e.detail.target.id === 'calls')) return;
    reindex();
    try {
      var info = e.detail.pathInfo || {};
      var path = info.responsePath || info.finalRequestPath || info.requestPath || '';
      var url = new URL(path, window.location.origin);
      if (url.pathname === '/calls') {
        url.pathname = '/';
        window.history.replaceState(null, '', url.toString());
      }
    } catch (err) {}
  });
  document.body.addEventListener('htmx:sendError', function () { say('The call list could not be loaded'); });
  document.body.addEventListener('htmx:timeout', function () { say('The call list request timed out'); });

  /* Keyboard controls: ignored while typing in form fields. */
  document.addEventListener('keydown', function (e) {
    var t = e.target;
    if (t && (t.tagName === 'INPUT' || t.tagName === 'SELECT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === ' ') { e.preventDefault(); toggleCurrent(); }
    else if (e.key === 'n') { if (index < queue.length - 1) playAt(index + 1, true); }
    else if (e.key === 'p') { if (index > 0) playAt(index - 1, true); }
    else if (e.key === 's') { stopPlayback(); bar.hidden = true; }
    else if (e.key === 'm') { audio.muted = !audio.muted; say(audio.muted ? 'Muted' : 'Unmuted'); }
  });

  reindex();
})();
