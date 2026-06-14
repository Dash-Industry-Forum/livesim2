// Live status page for Server-Guided Ad Insertion (SGAI). Polls the JSON API
// (/api/sgai/sessions[/<sid>]) every second and renders the recorded ad decisions and
// impression beacons. With no ?sid= it lists the active sessions. Intentionally
// dependency-free (plain fetch polling) so it works behind any proxy/CDN.
//
// Served as a static asset and loaded by templates/sgai_session_status.html, which carries
// the host (for proxy/base-path setups) on <body data-api>.
(() => {
  const API = document.body.dataset.api || '';
  const qs = new URLSearchParams(location.search);
  const sidInput = document.getElementById('sid');
  const resetBtn = document.getElementById('reset');
  sidInput.value = qs.get('sid') || '';
  let sid = sidInput.value.trim();
  resetBtn.disabled = !sid; // a reset targets one session id; disabled when listing all
  sidInput.addEventListener('change', () => {
    sid = sidInput.value.trim();
    resetBtn.disabled = !sid;
    const u = new URL(location.href);
    if (sid) u.searchParams.set('sid', sid); else u.searchParams.delete('sid');
    history.replaceState(null, '', u);
    refresh();
  });
  // Reset clears just this session's recorded decisions and beacons server-side, then
  // re-polls so the page shows the clean slate.
  resetBtn.addEventListener('click', async () => {
    if (!sid) return;
    try {
      await fetch(API + '/api/sgai/sessions/' + encodeURIComponent(sid) + '/clear',
        {method: 'POST'});
    } catch (e) { /* the next poll reflects the cleared state regardless */ }
    refresh();
  });

  const esc = (s) => String(s == null ? '' : s).replace(/[&<>]/g,
    (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
  const fmtTime = (t) => { try { return new Date(t).toLocaleTimeString(); } catch (e) { return t; } };

  function renderSession(s) {
    document.getElementById('meta').innerHTML =
      'sid <b>' + esc(s.sid) + '</b>' +
      (s.interests ? ' <span class="pill">interests: ' + esc(s.interests) + '</span>' : '') +
      ' &nbsp; decisions: <b>' + (s.decisionCount||0) + '</b>' +
      ' &nbsp; beacons: <b>' + (s.beaconCount||0) + '</b>';
    const evs = (s.events || []).slice().reverse(); // newest first
    if (!evs.length) { document.getElementById('body').innerHTML =
      '<div class="empty">No events yet for this session.</div>'; return; }
    let rows = '';
    for (const e of evs) {
      const detail = e.kind === 'decision'
        ? 'pod: <code>' + esc((e.pod||[]).join(' → ')) + '</code>'
          + (e.interests ? '  (interests ' + esc(e.interests) + ')' : '')
        : 'ad <code>' + esc(e.adId) + '</code> · ' + esc(e.event)
          + (e.evId ? '  (break <code>' + esc(e.evId) + '</code>)' : '')
          + (e.cmcd ? '  cmcd: <code>' + esc(e.cmcd) + '</code>' : '');
      rows += '<tr class="' + esc(e.kind) + '"><td>' + fmtTime(e.time) +
        '</td><td class="kind">' + esc(e.kind) + '</td><td>' + detail + '</td></tr>';
    }
    document.getElementById('body').innerHTML =
      '<table><thead><tr><th>time</th><th>kind</th><th>detail</th></tr></thead><tbody>'
      + rows + '</tbody></table>';
  }

  function renderList(list) {
    document.getElementById('meta').innerHTML =
      '<b>' + list.length + '</b> active session(s) — click a session id to follow it';
    if (!list.length) { document.getElementById('body').innerHTML =
      '<div class="empty">No active sessions. Play a stream with ?sessionId=… to populate this.</div>'; return; }
    let rows = '';
    for (const s of list) {
      rows += '<tr><td><a href="?sid=' + encodeURIComponent(s.sid) + '">' + esc(s.sid) + '</a></td><td>'
        + esc(s.interests || '') + '</td><td>' + (s.decisionCount||0) + '</td><td>'
        + (s.beaconCount||0) + '</td><td>' + fmtTime(s.lastSeen) + '</td></tr>';
    }
    document.getElementById('body').innerHTML =
      '<table><thead><tr><th>sid</th><th>interests</th><th>decisions</th><th>beacons</th><th>last seen</th></tr></thead><tbody>'
      + rows + '</tbody></table>';
  }

  async function refresh() {
    const dot = document.getElementById('dot');
    try {
      if (sid) {
        const r = await fetch(API + '/api/sgai/sessions/' + encodeURIComponent(sid));
        if (r.status === 404) {
          document.getElementById('meta').innerHTML = 'sid <b>' + esc(sid) + '</b> — not seen yet';
          document.getElementById('body').innerHTML =
            '<div class="empty">No activity recorded for this session id (yet, or it expired).</div>';
        } else {
          const d = await r.json();
          renderSession(d.session || d);
        }
      } else {
        const r = await fetch(API + '/api/sgai/sessions');
        const d = await r.json();
        renderList(d.sessions || []);
      }
      dot.textContent = '●'; dot.style.color = '#27ae60';
    } catch (e) {
      dot.textContent = '● offline'; dot.style.color = '#c0392b';
    }
  }
  refresh();
  setInterval(refresh, 1000);
})();
