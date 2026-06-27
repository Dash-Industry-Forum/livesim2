// Shared helpers for the live monitoring pages (sgai_session_status.js and
// steering_session_status.js). Loaded as a plain global via <script src> before the
// page-specific script — no module system, so it works behind any proxy/CDN.
(() => {
  // esc escapes for both HTML text and double-/single-quoted attribute contexts, so a value
  // can never break out of an attribute (esc() output is used in both).
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g,
    (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  // fmtTime renders a timestamp in the viewer's locale, falling back to the raw value.
  const fmtTime = (t) => { try { return new Date(t).toLocaleTimeString(); } catch (e) { return t; } };
  // setDot reflects poll connectivity in the header status dot. The colour comes from the
  // theme-aware .dot.online/.dot.offline rules in session_status.css.
  const setDot = (el, online) => {
    if (!el) return;
    el.textContent = online ? '●' : '● offline';
    el.className = online ? 'dot online' : 'dot offline';
  };
  window.SessionStatus = { esc, fmtTime, setDot };
})();
