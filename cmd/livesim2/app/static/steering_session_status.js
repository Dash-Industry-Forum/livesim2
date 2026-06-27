// Live status page for DASH Content Steering. Polls the JSON API every second and renders the
// per-CDN (service location) segment request distribution and the current PATHWAY-PRIORITY.
//
// Three views, selected by the query string:
//   ?csid=<group>  — a content-steering group: aggregate per-CDN counts, member sessions, and a
//                    "Switch group" control that moves every member together.
//   ?sid=<session> — one session: its per-CDN counts, current priority, and steering-poll timeline
//                    with the player's _DASH_pathway/_DASH_throughput messages and their verification.
//   (neither)      — a list of active groups and sessions.
// The header Switch/Reset buttons act on the group when a csid is set, otherwise on the session.
// Intentionally dependency-free (plain fetch polling) so it works behind any proxy/CDN.
//
// Served as a static asset and loaded by templates/steering_session_status.html, which carries the
// host (for proxy/base-path setups) on <body data-api>.
(() => {
  const { esc, fmtTime, setDot } = window.SessionStatus;
  const API = document.body.dataset.api || '';
  const qs = new URLSearchParams(location.search);
  const sidInput = document.getElementById('sid');
  const csidInput = document.getElementById('csid');
  const resetBtn = document.getElementById('reset');
  const switchBtn = document.getElementById('switch');
  sidInput.value = qs.get('sid') || '';
  csidInput.value = qs.get('csid') || '';
  let sid = sidInput.value.trim();
  let csid = csidInput.value.trim();

  // The view (and what the header buttons act on) is a group when csid is set, otherwise a session
  // when sid is set, otherwise the list. target() maps that to the API collection + id to act on.
  const target = () => csid ? {kind: 'groups', id: csid} : (sid ? {kind: 'sessions', id: sid} : null);
  const setBtns = () => {
    const t = target();
    resetBtn.disabled = !t;
    switchBtn.disabled = !t;
    switchBtn.textContent = csid ? 'Switch group' : 'Switch CDN';
    resetBtn.textContent = csid ? 'Reset group' : 'Reset session';
  };
  setBtns();

  function navState() {
    const u = new URL(location.href);
    if (sid) u.searchParams.set('sid', sid); else u.searchParams.delete('sid');
    if (csid) u.searchParams.set('csid', csid); else u.searchParams.delete('csid');
    history.replaceState(null, '', u);
  }
  sidInput.addEventListener('change', () => { sid = sidInput.value.trim(); setBtns(); navState(); refresh(); });
  csidInput.addEventListener('change', () => { csid = csidInput.value.trim(); setBtns(); navState(); refresh(); });

  resetBtn.addEventListener('click', async () => {
    const t = target();
    if (!t) return;
    try {
      await fetch(API + '/api/steering/' + t.kind + '/' + encodeURIComponent(t.id) + '/clear', {method: 'POST'});
    } catch (e) { /* the next poll reflects the cleared state regardless */ }
    refresh();
  });
  // doSwitch pins a new CDN priority for the current group or session via the API; clients pick it
  // up on their next steering poll (within TTL). promote is a service location to move to the top,
  // or 'next' to advance one step.
  async function doSwitch(promote) {
    const t = target();
    if (!t) return;
    try {
      await fetch(API + '/api/steering/' + t.kind + '/' + encodeURIComponent(t.id) + '/switch',
        {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({target: promote})});
    } catch (e) { /* the next poll reflects the new priority regardless */ }
    refresh();
  }
  switchBtn.addEventListener('click', () => doSwitch('next'));
  // Per-CDN "make top" buttons (rendered per row) promote a specific service location on the current
  // group or session.
  document.getElementById('body').addEventListener('click', (e) => {
    const t = e.target.closest('[data-switch]');
    if (t) doSwitch(t.getAttribute('data-switch'));
  });

  const sumCounts = (c) => Object.values(c || {}).reduce((a, b) => a + b, 0);
  // verifyBadge renders the conformance verdict for the player steering messages of a session or
  // group: a green tick when polls were verified with no issues, a red warning with the issue count
  // when a client sent a malformed message or ignored a steering decision, or a neutral dash before
  // any poll has been verified.
  const verifyBadge = (x) => {
    const n = x.issueCount || 0;
    if (n > 0) return '<span class="verify bad" title="client-message conformance issues">⚠ ' + n + '</span>';
    if (x.steeringRequestCount > 0) return '<span class="verify ok" title="client messages verified, no issues">✓</span>';
    return '<span class="verify none">–</span>';
  };

  // barTable renders the per-CDN segment-request bars in the configured order, highlighting the
  // current top pathway, with a "make top" button per non-top row (promotes via data-switch).
  function barTable(cdns, counts, priority) {
    const top = priority[0];
    const max = Math.max(1, ...Object.values(counts));
    let rows = '';
    for (const cdn of cdns) {
      const n = counts[cdn] || 0;
      const w = Math.round(220 * n / max);
      const isTop = cdn === top;
      const rank = priority.indexOf(cdn);
      const action = isTop ? '<span class="rank top">current</span>'
        : '<button class="switch" data-switch="' + esc(cdn) + '" title="Make ' + esc(cdn)
          + ' the top priority">make top</button>';
      rows += '<tr><td><code>' + esc(cdn) + '</code></td>'
        + '<td class="rank' + (isTop ? ' top' : '') + '">' + (rank >= 0 ? '#' + (rank + 1) : '-') + '</td>'
        + '<td><span class="bar' + (isTop ? ' top' : '') + '" style="width:' + w + 'px"></span> '
        + n + '</td><td>' + action + '</td></tr>';
    }
    return '<table><thead><tr><th>service location (CDN)</th><th>priority</th><th>segment requests</th>'
      + '<th>switch</th></tr></thead><tbody>' + rows + '</tbody></table>';
  }

  function renderSession(s) {
    const priority = s.currentPriority || [];
    const counts = s.segmentCounts || {};
    const cdns = (s.cdns && s.cdns.length) ? s.cdns : Object.keys(counts);
    const group = s.csid ? ' <span class="pill">group: <a href="?csid=' + encodeURIComponent(s.csid)
      + '">' + esc(s.csid) + '</a></span>' : '';
    const lastPoll = s.lastPolledAt ? ' &nbsp; last steering poll: ' + fmtTime(s.lastPolledAt) : '';
    const lastFetched = s.lastSegment || s.lastLocation
      ? ' &nbsp; last fetch: <code>' + esc(s.lastSegment || '?') + '</code> via <code>' + esc(s.lastLocation || '?')
        + '</code> at ' + fmtTime(s.lastSeen)
      : '';
    const offPathway = s.offPathway
      ? ' <span class="bad">⚠ off-pathway: fetching <code>' + esc(s.lastLocation || '?') + '</code> but steered to <code>'
        + esc(priority[0] || '?') + '</code></span>'
      : '';
    document.getElementById('meta').innerHTML =
      'sid <b>' + esc(s.sid) + '</b>' + group +
      ' <span class="pill mode">' + esc(s.mode || '?') + (s.manualOverride ? ' (override)' : '') + '</span>' +
      ' <span class="pill">priority: ' + esc(priority.join(' → ')) + '</span>' +
      ' &nbsp; steering polls: <b>' + (s.steeringRequestCount || 0) + '</b>' +
      ' &nbsp; verify: ' + verifyBadge(s) + offPathway + lastPoll + lastFetched;
    document.getElementById('body').innerHTML = barTable(cdns, counts, priority) + renderPolls(s.events || []);
  }

  function renderGroup(g) {
    const priority = g.currentPriority || [];
    const counts = g.segmentCounts || {};
    const cdns = (g.cdns && g.cdns.length) ? g.cdns : Object.keys(counts);
    document.getElementById('meta').innerHTML =
      'group <b>' + esc(g.csid) + '</b>' +
      ' <span class="pill mode">' + esc(g.mode || '?') + (g.manualOverride ? ' (override)' : '') + '</span>' +
      ' <span class="pill">priority: ' + esc(priority.join(' → ')) + '</span>' +
      ' &nbsp; members: <b>' + (g.memberCount || 0) + '</b>' +
      ' &nbsp; steering polls: <b>' + (g.steeringRequestCount || 0) + '</b>' +
      ' &nbsp; verify: ' + verifyBadge(g);
    // Aggregate per-CDN bars; "make top" here switches the whole group (csid is set, so doSwitch
    // hits the group endpoint).
    let html = barTable(cdns, counts, priority);
    const members = g.members || [];
    html += '<h2 class="polls">Members</h2>';
    if (!members.length) {
      html += '<div class="empty">No members seen yet.</div>';
    } else {
      let rows = '';
      for (const s of members) {
        const lastFetch = s.lastSegment ? '<code>' + esc(s.lastSegment) + '</code> via <code>'
          + esc(s.lastLocation || '?') + '</code>' : '';
        rows += '<tr><td><a href="?sid=' + encodeURIComponent(s.sid) + '">' + esc(s.sid) + '</a></td>'
          + '<td>' + sumCounts(s.segmentCounts) + '</td><td>' + (s.steeringRequestCount || 0) + '</td>'
          + '<td>' + verifyBadge(s) + '</td><td>' + lastFetch + '</td><td>' + fmtTime(s.lastSeen) + '</td></tr>';
      }
      html += '<table><thead><tr><th>sid</th><th>segments</th><th>polls</th><th>verify</th>'
        + '<th>last fetch</th><th>last seen</th></tr></thead><tbody>' + rows + '</tbody></table>';
    }
    html += renderSwitchLog(g.events || []);
    document.getElementById('body').innerHTML = html;
  }

  // renderSwitchLog shows the group's switch timeline (most recent first): when, and the priority
  // that was pinned for the whole group.
  function renderSwitchLog(events) {
    const evs = events.filter((e) => e.kind === 'switch');
    if (!evs.length) return '';
    let rows = '';
    for (const e of evs.slice().reverse().slice(0, 40)) {
      rows += '<tr><td>' + fmtTime(e.time) + '</td><td><code>' + esc((e.priority || []).join(' → ')) + '</code></td></tr>';
    }
    return '<h2 class="polls">Group switches</h2><table><thead><tr><th>time</th><th>priority set</th>'
      + '</tr></thead><tbody>' + rows + '</tbody></table>';
  }

  // renderPolls shows a session's steering-poll timeline (most recent first): the _DASH_pathway and
  // _DASH_throughput the player reported, the priority the server served back, and any conformance
  // issues found verifying that message. Rows with issues are highlighted. Switch events (no client
  // message) are shown for context but carry no verification.
  function renderPolls(events) {
    const polls = events.filter((e) => e.kind === 'steering' || e.kind === 'switch' || e.kind === 'offPathway');
    if (!polls.length) return '<div class="empty">No steering polls recorded yet.</div>';
    let rows = '';
    for (const e of polls.slice().reverse().slice(0, 40)) {
      const issues = e.issues || [];
      const bad = issues.length > 0;
      const detail = bad
        ? '<span class="bad">' + issues.map(esc).join('<br>') + '</span>'
        : (e.kind === 'switch' ? '<span class="rank">API switch</span>' : '<span class="ok">✓ ok</span>');
      rows += '<tr' + (bad ? ' class="badrow"' : '') + '><td>' + fmtTime(e.time) + '</td>'
        + '<td>' + esc(e.kind) + '</td>'
        + '<td><code>' + esc(e.pathway || '–') + '</code></td>'
        + '<td>' + esc(e.throughput || '–') + '</td>'
        + '<td><code>' + esc((e.priority || []).join(' → ')) + '</code></td>'
        + '<td>' + detail + '</td></tr>';
    }
    return '<h2 class="polls">Steering polls (client messages)</h2>'
      + '<table><thead><tr><th>time</th><th>kind</th><th>_DASH_pathway</th><th>_DASH_throughput</th>'
      + '<th>priority served</th><th>verification</th></tr></thead><tbody>'
      + rows + '</tbody></table>';
  }

  function renderList(groups, sessions) {
    document.getElementById('meta').innerHTML =
      '<b>' + groups.length + '</b> group(s), <b>' + sessions.length + '</b> session(s) — click a csid or sid to follow it';
    let html = '';
    if (groups.length) {
      let grows = '';
      for (const g of groups) {
        grows += '<tr><td><a href="?csid=' + encodeURIComponent(g.csid) + '">' + esc(g.csid) + '</a></td>'
          + '<td>' + (g.memberCount || 0) + '</td><td>' + esc(g.mode || '') + '</td>'
          + '<td><code>' + esc((g.currentPriority || []).join(' → ')) + '</code></td>'
          + '<td>' + sumCounts(g.segmentCounts) + '</td><td>' + (g.steeringRequestCount || 0) + '</td>'
          + '<td>' + verifyBadge(g) + '</td><td>' + fmtTime(g.lastSeen) + '</td></tr>';
      }
      html += '<h2 class="polls">Groups</h2><table><thead><tr><th>csid</th><th>members</th><th>mode</th>'
        + '<th>priority</th><th>segments</th><th>polls</th><th>verify</th><th>last seen</th></tr></thead><tbody>'
        + grows + '</tbody></table>';
    }
    html += '<h2 class="polls">Sessions</h2>';
    if (!sessions.length) {
      html += '<div class="empty">No active sessions. Play a steer_ stream with ?sessionId=… '
        + '(and optionally a csid_ group token) to populate this.</div>';
    } else {
      let rows = '';
      for (const s of sessions) {
        const grp = s.csid ? '<a href="?csid=' + encodeURIComponent(s.csid) + '">' + esc(s.csid) + '</a>' : '';
        rows += '<tr><td><a href="?sid=' + encodeURIComponent(s.sid) + '">' + esc(s.sid) + '</a></td>'
          + '<td>' + grp + '</td><td>' + esc(s.mode || '') + '</td>'
          + '<td><code>' + esc((s.currentPriority || []).join(' → ')) + '</code></td>'
          + '<td>' + sumCounts(s.segmentCounts) + '</td><td>' + (s.steeringRequestCount || 0) + '</td>'
          + '<td>' + (s.lastPolledAt ? fmtTime(s.lastPolledAt) : '–') + '</td>'
          + '<td>' + verifyBadge(s) + '</td><td>' + fmtTime(s.lastSeen) + '</td></tr>';
      }
      html += '<table><thead><tr><th>sid</th><th>csid</th><th>mode</th><th>priority</th><th>segments</th>'
        + '<th>polls</th><th>last poll</th><th>verify</th><th>last seen</th></tr></thead><tbody>' + rows + '</tbody></table>';
    }
    document.getElementById('body').innerHTML = html;
  }

  async function refresh() {
    const dot = document.getElementById('dot');
    try {
      if (csid) {
        const r = await fetch(API + '/api/steering/groups/' + encodeURIComponent(csid));
        if (r.status === 404) {
          document.getElementById('meta').innerHTML = 'group <b>' + esc(csid) + '</b> — not seen yet';
          document.getElementById('body').innerHTML =
            '<div class="empty">No content-steering group with this id (yet, or it expired).</div>';
        } else {
          const d = await r.json();
          renderGroup(d.group || d);
        }
      } else if (sid) {
        const r = await fetch(API + '/api/steering/sessions/' + encodeURIComponent(sid));
        if (r.status === 404) {
          document.getElementById('meta').innerHTML = 'sid <b>' + esc(sid) + '</b> — not seen yet';
          document.getElementById('body').innerHTML =
            '<div class="empty">No content-steering activity for this session id (yet, or it expired).</div>';
        } else {
          const d = await r.json();
          renderSession(d.session || d);
        }
      } else {
        const [gr, sr] = await Promise.all([
          fetch(API + '/api/steering/groups'),
          fetch(API + '/api/steering/sessions'),
        ]);
        const gd = await gr.json();
        const sd = await sr.json();
        renderList(gd.groups || [], sd.sessions || []);
      }
      setDot(dot, true);
    } catch (e) {
      setDot(dot, false);
    }
  }
  refresh();
  setInterval(refresh, 1000);
})();
