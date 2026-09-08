// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.
//
// Synthetic ad-verification resource for the SVTA2053 ad creative signaling (svta_ URL
// option with ;verif=1). It stands in for an OMID verification script so that a player which
// loads verification resources has something real to fetch. It collects no data and reports
// nothing back; it only logs that it ran, together with the query parameters livesim2 passed
// as the verification "parameters" string (adId and evId).
(function () {
  var params = {};
  try {
    var src = (document.currentScript && document.currentScript.src) || '';
    var query = src.indexOf('?') >= 0 ? src.slice(src.indexOf('?') + 1) : '';
    new URLSearchParams(query).forEach(function (value, key) {
      params[key] = value;
    });
  } catch (e) {
    // Nothing to do: the log line below is still useful without the parameters.
  }
  console.log('livesim2 SVTA2053 verification resource loaded', params);
})();
