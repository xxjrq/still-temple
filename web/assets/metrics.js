/* 静寺匿名埋点：只记"进来没、待多久、切了几次房"。不存 cookie、不发 IP。 */
(function () {
  var API = '/api/event';
  var lang = (document.documentElement.lang || 'zh').slice(0, 2);
  var t0 = Date.now();
  var lastRoom = 0;

  function roomNo() {
    var m = /room=maru-([0-9]+)/.exec(location.search);
    return m ? parseInt(m[1], 10) : 1;
  }

  function send(event, extra) {
    var body = JSON.stringify(Object.assign({ e: event, lang: lang, room: roomNo() }, extra || {}));
    try {
      if (navigator.sendBeacon) navigator.sendBeacon(API, new Blob([body], { type: 'application/json' }));
      else fetch(API, { method: 'POST', body: body, keepalive: true, headers: { 'Content-Type': 'application/json' } });
    } catch (_) {}
  }

  send('enter');

  // 切房间：拦 history.replaceState / pushState
  ['replaceState', 'pushState'].forEach(function (fn) {
    var orig = history[fn];
    history[fn] = function () {
      var r = roomNo();
      if (r !== lastRoom) {
        lastRoom = r;
        send('room', { room: r });
      }
      return orig.apply(this, arguments);
    };
  });

  document.addEventListener('visibilitychange', function () {
    if (document.hidden) send('hide', { d: Math.round((Date.now() - t0) / 1000) });
  });

  window.addEventListener('pagehide', function () {
    send('leave', { d: Math.round((Date.now() - t0) / 1000) });
  });
})();
