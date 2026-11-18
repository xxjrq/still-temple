/* 房间模块：房号、切房、切语言保房。
   原实现是页面内联的 mirror-patch，现收进独立模块，七语言共用一份。
   没有实时链路时也能用：点房间 → 换 URL → 重连（reload），不依赖 WebSocket 握手。 */
(function () {
  var ROOM_RE = /room=maru(?:-(\d+))?/;
  var liveRooms = Object.create(null);
  var liveRoomsReady = false;
  var liveDialogOpen = false;
  var liveRefreshTimer = 0;
  var liveRoomLabels = {
    ko: ['실시간 {n} / {c}명', '실시간 인원 확인 불가', '실시간 인원 확인 중'],
    zh: ['实时 {n} / {c} 人', '实时人数暂不可用', '正在查询实时人数'],
    ja: ['リアルタイム {n} / {c} 人', 'リアルタイム人数を取得できません', 'リアルタイム人数を確認中'],
    en: ['Live {n} / {c}', 'Live count unavailable', 'Checking live count'],
    es: ['En línea {n} / {c}', 'Recuento en línea no disponible', 'Consultando el recuento'],
    fr: ['En direct {n} / {c}', 'Nombre en direct indisponible', 'Vérification du nombre'],
    'zh-hant': ['即時 {n} / {c} 人', '暫時無法取得即時人數', '正在查詢即時人數']
  }[(document.documentElement.lang || 'en').toLowerCase()] || ['Live {n} / {c}', 'Live count unavailable', 'Checking live count'];

  function refreshLiveRooms() {
    fetch('/api/rooms', { cache: 'no-store' }).then(function (r) {
      if (!r.ok) throw new Error('room directory unavailable');
      return r.json();
    }).then(function (data) {
      liveRooms = Object.create(null);
      (data.rooms || []).forEach(function (room) { liveRooms[room.id] = room; });
      liveRoomsReady = true;
    }).catch(function () { liveRooms = Object.create(null); liveRoomsReady = true; });
  }

  function checkRoomDialog() {
    var dialog = document.getElementById('room-dialog');
    var open = !!(dialog && dialog.open);
    if (open && !liveDialogOpen) {
      liveDialogOpen = true;
      liveRoomsReady = false;
      refreshLiveRooms();
      liveRefreshTimer = setInterval(refreshLiveRooms, 10000);
    } else if (!open && liveDialogOpen) {
      liveDialogOpen = false;
      clearInterval(liveRefreshTimer);
      liveRefreshTimer = 0;
    }
  }

  function roomNumber() {
    var m = location.search.match(ROOM_RE);
    return m ? (m[1] ? parseInt(m[1], 10) : 1) : 1;
  }

  function roomId(n) {
    return n === 1 ? 'maru' : 'maru-' + String(n).padStart(2, '0');
  }

  function goRoom(n) {
    var u = new URL(location.href);
    u.searchParams.set('room', roomId(n));
    u.searchParams.set('instance', '0');
    u.searchParams.set('region', 'apac');
    location.href = u.toString();
  }

  /* 客户端没有 WebSocket 时房间按钮一直是 disabled；这里解开并接管点击 */
  function bindRooms() {
    var cur = roomNumber();
    var list = document.querySelectorAll('.room-choice');
    for (var i = 0; i < list.length; i++) {
      var b = list[i];
      var strong = b.querySelector('strong');
      var m = strong ? (strong.textContent || '').match(/(\d+)/) : null;
      if (!m) continue;
      var n = parseInt(m[1], 10);
      var record = liveRooms[roomId(n)];
      var detail = b.querySelector('.room-choice-detail');
      if (detail && liveDialogOpen) {
        var detailText = record && Number.isFinite(record.liveCount)
          ? liveRoomLabels[0].replace('{n}', record.liveCount).replace('{c}', record.capacity || 100)
          : (liveRoomsReady ? liveRoomLabels[1] : liveRoomLabels[2]);
        if (detail.textContent !== detailText) detail.textContent = detailText;
      }
      var ariaLabel = (strong.textContent || '').trim() + (detail && detail.textContent ? ': ' + detail.textContent.trim() : '');
      if (b.getAttribute('aria-label') !== ariaLabel) b.setAttribute('aria-label', ariaLabel);
      b.disabled = n === cur || !!(liveDialogOpen && liveRoomsReady && record && record.liveCount >= (record.capacity || 100));
      if (n === cur) continue;
      if (b.dataset.mirrorBound) continue;
      b.dataset.mirrorBound = '1';
      b.addEventListener('click', function (e) {
        var s = this.querySelector('strong');
        var mm = s ? (s.textContent || '').match(/(\d+)/) : null;
        if (!mm) return;
        e.preventDefault();
        e.stopImmediatePropagation();
        goRoom(parseInt(mm[1], 10));
      }, true);
    }
  }

  /* 语言切换是整页跳转，把当前房间带上，不然会回到第 1 间 */
  function fixLangLinks() {
    var cur = roomNumber();
    var links = document.querySelectorAll('a[hreflang]');
    for (var i = 0; i < links.length; i++) {
      var a = links[i];
      var href = a.getAttribute('href');
      if (!href) continue;
      var u = new URL(href, location.origin);
      u.searchParams.set('room', roomId(cur));
      u.searchParams.set('instance', '0');
      u.searchParams.set('region', 'apac');
      var want = u.pathname + u.search;
      if (a.getAttribute('href') !== want) a.setAttribute('href', want);
    }
  }

  function bindLanguageMenu() {
    var toggle = document.querySelector('.lang-toggle');
    var list = document.querySelector('.lang-list');
    if (!toggle || !list || toggle.dataset.roomLangBound) return;
    toggle.dataset.roomLangBound = '1';
    function close() { list.hidden = true; toggle.setAttribute('aria-expanded', 'false'); }
    toggle.addEventListener('click', function (event) {
      event.preventDefault();
      var open = toggle.getAttribute('aria-expanded') !== 'true';
      list.hidden = !open;
      toggle.setAttribute('aria-expanded', String(open));
    });
    document.addEventListener('click', function (event) {
      if (!event.target.closest('.lang-pill')) close();
    });
    document.addEventListener('keydown', function (event) {
      if (event.key === 'Escape' && !list.hidden) { close(); toggle.focus(); }
    });
  }

  /* 人数语义占掉了 NN/100，房间号补回标题后面 */
  function paintRoom() {
    var roomEl = document.getElementById('game-room');
    var roomNumberEl = document.getElementById('game-room-number');
    if (roomNumberEl) {
      var numberText = '#' + roomNumber();
      if (roomNumberEl.textContent !== numberText) roomNumberEl.textContent = numberText;
    } else if (roomEl) {
      if (!roomEl.dataset.mirrorBase) roomEl.dataset.mirrorBase = (roomEl.textContent || '').trim();
      var want = roomEl.dataset.mirrorBase + ' #' + roomNumber();
      if (roomEl.textContent !== want) roomEl.textContent = want;
    }
    var conn = document.getElementById('game-connection');
    if (conn && conn.style.display !== 'none') conn.style.display = 'none';
    bindRooms();
    bindLanguageMenu();
    fixLangLinks();
  }

  var tries = 0;
  function boot() {
    if (!document.querySelector('canvas') && document.body.innerText.indexOf('Could not load') < 0) {
      if (tries++ < 120) return setTimeout(boot, 500);
    }
    if (document.head && !document.getElementById('room-style')) {
      var st = document.createElement('style');
      st.id = 'room-style';
      st.textContent = '#game-connection{display:none!important}';
      document.head.appendChild(st);
    }
    paintRoom();
    setInterval(paintRoom, 600);
  }

  window.ST = window.ST || {};
  window.ST.room = { number: roomNumber(), id: roomId(roomNumber()) };
  setInterval(checkRoomDialog, 1000);
  boot();
})();
