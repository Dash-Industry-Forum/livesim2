// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.
//
// livesim2 SGAI demo: one live stream in two players, each getting its own personalized ad pod
// at every break. Everything the page talks to is on the livesim2 server that serves it; ?ls=
// points it at another livesim2 instead (livesim2 allows any origin).
'use strict';

(() => {
  const params = new URLSearchParams(location.search);
  // The livesim2 base URL. It is always absolute: Shaka resolves a root-relative MPD URL wrongly
  // when its path has a colon in it, as in sgai_p60:20.
  const ls = params.get('ls') || location.origin;
  // The sgai_ break schedule. Default: a 20 s break at every full UTC minute.
  const breaks = params.get('breaks') || 'p60:20';
  const periodic = breaks.startsWith('p');
  // Duration (s) of the first break. The pod preview must ask for the same dur as the
  // player's ad request, or the two pods differ.
  const breakDurS = parseInt(breaks.split(';')[0].split(',')[0].split(':')[1], 10) || 20;
  // Main content candidates, best first. testrotate_2s is one hour long and its burned-in
  // clock shows UTC, so viewers see it reach :00 as the pod starts. The bundled test content
  // has only the 8 s testpic_2s.
  const assetCandidates = ['testrotate_2s', 'testpic_2s'];
  const viewers = [
    {name: 'alice', defaultInterest: 'travel'},
    {name: 'bob', defaultInterest: 'sailing'},
  ];
  const bannerColors = ['#c0392b', '#27ae60', '#2980b9'];

  // Player adapters. All player-specific code is here, so that another player (dash.js, once
  // it executes Alternative-MPD events) can be added next to Shaka.
  const shakaAdapter = {
    name: 'Shaka Player',
    supported: () => typeof shaka !== 'undefined' && shaka.Player.isBrowserSupported(),
    version: () => shaka.Player.version,
    init: () => shaka.polyfill.installAll(),
    // create attaches a player to video and reports the ad events through cb.
    async create(video, cb) {
      const player = new shaka.Player();
      await player.attach(video);
      const am = player.getAdManager();
      // timeOffset is the media presentation time of the break, in seconds.
      am.addEventListener('ad-break-started', (e) => cb.breakStarted(e.timeOffset));
      // Shaka plays a whole List-MPD pod as one interstitial, so ad-started fires once per pod.
      am.addEventListener('ad-started', (e) => cb.adStarted(e.ad));
      am.addEventListener('ad-break-ended', () => cb.breakEnded());
      player.addEventListener('error', (e) => cb.error(e.detail && e.detail.code));
      return {load: (url) => player.load(url), destroy: () => player.destroy()};
    },
  };
  const adapter = shakaAdapter;

  const notice = (text) => {
    const el = document.getElementById('notice');
    el.textContent = (el.textContent ? el.textContent + ' ' : '') + text;
    el.hidden = false;
  };

  // pickAsset returns ?asset= or the first candidate that this server has.
  async function pickAsset() {
    if (params.get('asset')) {
      return params.get('asset');
    }
    for (const asset of assetCandidates) {
      try {
        const r = await fetch(`${ls}/vod/${asset}/Manifest.mpd`, {method: 'HEAD'});
        if (r.ok) {
          return asset;
        }
      } catch (e) {
        // Try the next candidate.
      }
    }
    return null;
  }

  // loadCatalog returns the ad creatives with their interest tags and durations.
  async function loadCatalog() {
    try {
      const r = await fetch(`${ls}/api/sgai/ads`);
      return r.ok ? ((await r.json()).ads || []) : [];
    } catch (e) {
      return [];
    }
  }

  // fetchPod asks the ad decisioning for the pod a viewer gets: the same request the player
  // makes, marked preview=1 so that livesim2 does not record it as a decision. No interests,
  // or none that match, means no pod.
  async function fetchPod(sid, interest) {
    if (!interest) {
      return [];
    }
    const q = new URLSearchParams({sessionId: sid, interests: interest, dur: String(breakDurS), preview: '1'});
    try {
      const r = await fetch(`${ls}/sgai/ads.mpd?${q}`);
      if (!r.ok) {
        return [];
      }
      const doc = new DOMParser().parseFromString(await r.text(), 'application/xml');
      return [...doc.getElementsByTagName('ImportedMPD')]
          .map((el) => (el.textContent.match(/\/vod\/ads\/([^/]+)\//) || [])[1])
          .filter(Boolean);
    } catch (e) {
      return [];
    }
  }

  function mpdURL(asset, sid, interest) {
    // Periodic breaks are anchored to the wall clock, so the stream keeps its default
    // epoch-anchored timeline. A fixed schedule counts from the availabilityStartTime, so the
    // stream then starts 12 s ago to put the first break just ahead of the live edge.
    const start = periodic ? '' : `/start_${Math.floor(Date.now() / 1000) - 12}`;
    let url = `${ls}/livesim2${start}/sgai_${breaks}/${asset}/Manifest.mpd?sessionId=${encodeURIComponent(sid)}`;
    if (interest) {
      url += `&interests=${encodeURIComponent(interest)}`;
    }
    return url;
  }

  // breakLabel shows when a break starts: the UTC time for epoch-anchored periodic breaks,
  // otherwise the offset from the stream start.
  const breakLabel = (t) => {
    if (typeof t !== 'number') {
      return '';
    }
    return periodic ? new Date(t * 1000).toISOString().slice(11, 19) + ' UTC' : `${Math.round(t)} s`;
  };

  // Viewer is one player with its banner, cover, log and session monitor.
  class Viewer {
    constructor(idx, ctx) {
      this.ctx = ctx;
      const root = document.getElementById('viewer' + idx);
      this.video = root.querySelector('video');
      this.banner = root.querySelector('.banner');
      this.cover = root.querySelector('.cover');
      this.logEl = root.querySelector('.log');
      this.mpdLink = root.querySelector('.mpd');
      // A new session id per page load, so that visitors don't share session records.
      this.sid = `${viewers[idx].name}-${Math.random().toString(36).slice(2, 6)}`;
      root.querySelector('.name').textContent = this.sid;
      root.querySelector('.status').src = `${ls}/sgai/session_status?sid=${encodeURIComponent(this.sid)}`;

      const select = root.querySelector('.interests');
      select.add(new Option('none (no ad pod)', ''));
      for (const tag of ctx.tags) {
        select.add(new Option(tag, tag));
      }
      const fromURL = params.get('i' + idx);
      let interest = ctx.tags.includes(viewers[idx].defaultInterest) ?
        viewers[idx].defaultInterest : (ctx.tags[idx] || '');
      if (fromURL !== null) {
        interest = fromURL;
        if (![...select.options].some((o) => o.value === fromURL)) {
          select.add(new Option(fromURL, fromURL));
        }
      }
      select.value = interest;
      select.addEventListener('change', () => this.load(select.value));
      this.load(interest);
    }

    log(line) {
      this.logEl.textContent += line + '\n';
      this.logEl.scrollTop = this.logEl.scrollHeight;
    }

    // load (re)starts the player for the given interests.
    async load(interest) {
      clearInterval(this.stepTimer);
      this.adActive = false;
      this.banner.classList.remove('on');
      this.cover.style.display = 'none';
      if (this.player) {
        await this.player.destroy();
      }
      this.logEl.textContent = '';
      this.pod = await fetchPod(this.sid, interest);
      this.noAds = this.pod.length === 0;
      const url = mpdURL(this.ctx.asset, this.sid, interest);
      this.mpdLink.href = url;
      this.player = await adapter.create(this.video, {
        breakStarted: (t) => this.breakStarted(t),
        adStarted: (ad) => this.adStarted(ad),
        breakEnded: () => this.breakEnded(),
        error: (code) => this.log(`player error ${code}`),
      });
      try {
        await this.player.load(url);
        if (!this.noAds) {
          this.log(`playing; ad pod: ${this.pod.join(' → ')}`);
        } else if (interest) {
          this.log(`playing; no ad matches "${interest}", so breaks show the AD BREAK slate`);
        } else {
          this.log('playing; no interests, so breaks show the AD BREAK slate');
        }
      } catch (e) {
        this.log(`load failed: ${e && e.code}`);
      }
    }

    breakStarted(t) {
      if (this.noAds) {
        // The decisioning answered 404, so the player executes nothing and the slate plays.
        this.log(`AD BREAK ${breakLabel(t)}: no ad pod, the AD BREAK slate plays`);
        return;
      }
      this.adActive = true;
      this.currentAd = -1;
      this.showCover();
      this.log(`AD BREAK ${breakLabel(t)}`);
    }

    adStarted(ad) {
      if (this.noAds) {
        return;
      }
      this.ad = ad;
      this.adStartWall = performance.now();
      this.hideCoverAfter(150);
      this.step();
      clearInterval(this.stepTimer);
      this.stepTimer = setInterval(() => this.step(), 250);
    }

    breakEnded() {
      if (this.noAds || !this.adActive) {
        return;
      }
      this.adActive = false;
      clearInterval(this.stepTimer);
      this.banner.classList.remove('on');
      this.showCover();
      this.hideCoverAfter(700);
      this.log('back to live');
    }

    // step shows the creative that is playing now. The player reports the pod as one ad, so
    // the position in the pod comes from the elapsed time and the creatives' durations.
    step() {
      let elapsedS = (performance.now() - this.adStartWall) / 1000;
      const ad = this.ad;
      if (ad && typeof ad.getDuration === 'function' && typeof ad.getRemainingTime === 'function') {
        const dur = ad.getDuration();
        const remaining = ad.getRemainingTime();
        if (isFinite(dur) && dur > 0 && isFinite(remaining) && remaining >= 0) {
          elapsedS = dur - remaining;
        }
      }
      const durS = (id) => ((this.ctx.byId[id] && this.ctx.byId[id].durationMs) || 10000) / 1000;
      let i = 0;
      let end = durS(this.pod[0]);
      while (i < this.pod.length - 1 && elapsedS >= end) {
        i++;
        end += durS(this.pod[i]);
      }
      if (i === this.currentAd) {
        return;
      }
      this.currentAd = i;
      const id = this.pod[i];
      const title = (this.ctx.byId[id] && this.ctx.byId[id].title) || id;
      this.banner.style.background = bannerColors[i % bannerColors.length];
      this.banner.textContent = `▶ AD ${i + 1}/${this.pod.length}: ${title}`;
      this.banner.classList.add('on');
      this.log(`AD ${i + 1}/${this.pod.length} ${id}`);
    }

    showCover() {
      this.cover.style.display = 'block';
      clearTimeout(this.coverFailsafe);
      this.coverFailsafe = setTimeout(() => {
        this.cover.style.display = 'none';
      }, 2500);
    }

    // hideCoverAfter hides the cover after minMs and a freshly presented video frame, so that
    // the new stream is really showing.
    hideCoverAfter(minMs) {
      const t0 = performance.now();
      const tryHide = () => {
        if (performance.now() - t0 >= minMs) {
          this.cover.style.display = 'none';
          clearTimeout(this.coverFailsafe);
        } else if (this.video.requestVideoFrameCallback) {
          this.video.requestVideoFrameCallback(tryHide);
        } else {
          setTimeout(tryHide, minMs);
        }
      };
      tryHide();
    }
  }

  async function main() {
    document.getElementById('breaks').textContent = breaks;
    for (const a of document.querySelectorAll('a[data-path]')) {
      a.href = ls + a.dataset.path;
    }
    if (typeof shaka === 'undefined') {
      notice('Shaka Player could not be loaded from cdn.jsdelivr.net.');
      return;
    }
    if (!adapter.supported()) {
      notice(`${adapter.name} does not support this browser.`);
      return;
    }
    adapter.init();
    document.getElementById('playerVersion').textContent = adapter.version();
    const [asset, catalog] = await Promise.all([pickAsset(), loadCatalog()]);
    if (!asset) {
      notice(`This server has none of ${assetCandidates.join(', ')}. Choose the main content with ?asset=.`);
      return;
    }
    document.getElementById('asset').textContent = asset;
    if (catalog.length === 0) {
      notice('This server has no ad creatives (<vodroot>/ads/), so every break shows the AD BREAK slate.');
    }
    const tags = [...new Set(catalog.flatMap((ad) => ad.interests || []))];
    const byId = Object.fromEntries(catalog.map((ad) => [ad.id, ad]));
    const ctx = {asset, tags, byId};
    viewers.forEach((_, idx) => new Viewer(idx, ctx));
  }

  main();
})();
