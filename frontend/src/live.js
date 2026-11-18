/* 最小真人同房模块。
   职责：WebSocket 进房、匿名访客、真实在线人数、位置同步、合掌/礼佛同步；
        氛围香客单独渲染并单独标注，不再把固定人数当在线人数。
   不做：座位/蒲团语义、诵经同播、真人换房不刷新（首版走 URL 切换）。 */
(function () {
  'use strict';

  var RoomCapacity = 100;
  var AMBIENT_COUNT = 3;
  var SEND_MS = 100;

  var lang = (document.documentElement.lang || 'zh').toLowerCase();
  var AMBIENT_LABEL = {
    zh: '模拟香客', 'zh-hant': '模擬香客', ja: '演出の参拝客', ko: '배경 참배객',
    en: 'ambient visitors', es: 'visitantes simulados', fr: 'visiteurs simulés'
  }[lang] || 'monks';

  var state = {
    ws: null,
    connected: false,
    room: (window.ST && window.ST.room && window.ST.room.id) || 'maru',
    id: '',
    count: -1,
    peers: {},
    game: null,
    greeted: false,
    bow: 'idle',
    retry: 0,
    lastPose: { x: 0, z: 0, ry: 0, mode: 'walk' },
    pendingPeers: [],
    seats: [],
    seatLeaseId: '',
    seatRequest: null,
    nextSeatRequestId: 0,
    incenseQueue: { revision: 0, members: [], activeId: null, expiresAt: 0 },
    incenseTurnId: '',
    incenseStarted: null,
    offerings: [],
    incenseRequests: Object.create(null),
    expiredIncenseRequests: Object.create(null),
    incenseLeaveSerial: 0,
    nextIncenseRequestId: 0,
    heartbeat: 0,
    lastSentPose: null,
    serverOffset: 0
  };

  function apiBase() {
    if (window.ST_API_ORIGIN) return window.ST_API_ORIGIN;
    try { if (window.Capacitor && window.Capacitor.isNativePlatform && window.Capacitor.isNativePlatform()) return 'https://stilltemple.app'; } catch (e) {}
    if (location.protocol === 'http:' || location.protocol === 'https:') return location.origin;
    return 'https://stilltemple.app';
  }

  function wsURL() {
    var base = apiBase().replace(/^http/, 'ws');
    var visitor = '';
    try { visitor = localStorage.getItem('st.visitor') || ''; } catch (e) {}
    return base + '/ws?room=' + encodeURIComponent(state.room) + (visitor ? '&visitor=' + encodeURIComponent(visitor) : '');
  }

  /* ---------- 远端真人角色：与本地香客复用同一完整角色工厂 ---------- */

  function spawnPeer(peer) {
    var runtime = window.sansaAppearanceRuntime;
    if (!state.game || !runtime || typeof runtime.createAmbientActor !== 'function') return;
    if (state.peers[peer.id]) return;
    var av = runtime.createAmbientActor();
    av.group.name = 'live-peer-' + peer.id;
    av.actorReady = false;
    av.mode = validMode(peer.mode) ? peer.mode : 'walk';
    av.setMode(av.mode);
    av.group.position.set(peer.x || 0, state.game.floorHeight(peer.x || 0, peer.z || 0), peer.z || 0);
    av.group.rotation.y = peer.ry || 0;
    av.target = { x: peer.x || 0, z: peer.z || 0, ry: peer.ry || 0, mode: av.mode };
    av.greetWant = !!peer.greet;
    av.bowWant = peer.bow === 'repeating';
    av.greetAt = peer.greetAt || 0;
    av.bowAt = peer.bowAt || 0;
    state.game.scene.add(av.group);
    state.peers[peer.id] = av;
    av.ready.then(function () {
      if (state.peers[peer.id] !== av || !state.game || av.group.parent !== state.game.scene) return;
      av.actorReady = true;
      var now = state.game.time || 0;
      var motion = av.captureMotion();
      motion.mode = av.mode;
      motion.stance = av.mode === 'walk' || av.mode === 'dance' ? 1 : 0;
      motion.bowStarted = null;
      motion.greetingStarted = null;
      av.restoreMotion(motion);
      av.lastUpdateAt = now;
      if (state.incenseStarted) {
        var started = state.incenseStarted;
        state.incenseStarted = null;
        applyStarted(started);
      }
      var greetAge = av.greetAt ? Math.max(0, (serverNow() - av.greetAt) / 1000) : 0;
      var bowAge = av.bowAt ? Math.max(0, (serverNow() - av.bowAt) / 1000) : 0;
      if (av.greetWant && greetAge < 3.1) av.greet(now - greetAge);
      if (av.bowWant && bowAge < 7) av.bow(now - bowAge);
      av.update(now, 0, 0);
    }).catch(function (error) {
      if (window.console && console.warn) console.warn('Live visitor model failed to load.', error);
    });
  }

  function localGestureTime(serverAt, now) {
    return serverAt ? now - Math.max(0, (serverNow() - serverAt) / 1000) : now;
  }

  function serverNow() { return Date.now() + state.serverOffset; }

  function validMode(mode) {
    return mode === 'walk' || mode === 'kneel' || mode === 'beads' || mode === 'dance';
  }

  function applyPeerMode(av, mode) {
    if (!validMode(mode) || av.mode === mode) return;
    av.mode = mode;
    if (!av.actorReady) return;
    var motion = av.captureMotion();
    motion.mode = mode;
    motion.stance = mode === 'walk' || mode === 'dance' ? 1 : 0;
    av.restoreMotion(motion);
  }

  function syncPeerAct(av, greet, bow, greetAt, bowAt) {
    if (!av) return;
    var wasGreeting = av.greetWant;
    var wasBowing = av.bowWant;
    av.greetWant = !!greet;
    av.bowWant = bow === 'repeating';
    av.greetAt = greetAt || 0;
    av.bowAt = bowAt || 0;
    if (av.greetWant && !wasGreeting) {
      if (av.actorReady) av.greet(localGestureTime(greetAt, state.game ? state.game.time : 0));
    } else if (!av.greetWant && wasGreeting) av.cancelGreeting();
    if (av.bowWant && !wasBowing) {
      if (av.actorReady) av.bow(localGestureTime(bowAt, state.game ? state.game.time : 0));
    }
  }

  function dropPeer(id) {
    var av = state.peers[id];
    if (!av) return;
    av.destroy();
    delete state.peers[id];
  }

  /* ---------- 氛围香客 ---------- */

  var ambient = null;
  var AMBIENT_SPOTS = [
    [3.15, 38.4, 3.0],
    [-2.05, 35.4, 2.9],
    [2.1, 31.4, 3.2]
  ];

  function spawnAmbient(game) {
    var runtime = window.sansaAppearanceRuntime;
    if (ambient || !runtime || typeof runtime.createAmbientActor !== 'function') return;
    ambient = [];
    for (var i = 0; i < AMBIENT_SPOTS.length; i++) {
      var s = AMBIENT_SPOTS[i];
      var actor = runtime.createAmbientActor();
      actor.group.name = 'ambient-visitor-' + (i + 1);
      actor.group.position.set(s[0], game.floorHeight(s[0], s[1]), s[1]);
      actor.group.rotation.y = s[2];
      actor.setMode('walk');
      game.scene.add(actor.group);
      ambient.push(actor);
      (function (resident) {
        resident.ready.then(function () {
          if (!game.disposed && resident.group.parent === game.scene) {
            var motion = resident.captureMotion();
            motion.mode = 'walk';
            motion.stance = 1;
            resident.restoreMotion(motion);
            resident.update(game.time, 0, 0);
          }
        }).catch(function (error) {
          if (window.console && console.warn) console.warn('Ambient visitor model failed to load.', error);
        });
      })(actor);
    }
  }

  function destroyAmbient() {
    if (!ambient) return;
    ambient.forEach(function (actor) { actor.destroy(); });
    ambient = null;
  }

  function ambientLabel() {
    var n = AMBIENT_COUNT;
    var el = document.getElementById('st-ambient-label');
    if (!el) {
      var roomEl = document.getElementById('game-room');
      if (!roomEl || !roomEl.parentNode) return;
      el = document.createElement('span');
      el.id = 'st-ambient-label';
      el.style.cssText = 'font-size:11px;opacity:.55;margin-left:6px;white-space:nowrap';
      roomEl.parentNode.insertBefore(el, roomEl.nextSibling);
    }
    var text = AMBIENT_LABEL + ' ' + n;
    if (el.textContent !== text) el.textContent = text;
  }

  /* ---------- 人数 ---------- */

  function paintCount() {
    var el = document.getElementById('game-presence-count');
    if (!el) return;
    var want = state.count < 0
      ? '—/' + RoomCapacity
      : String(state.count).padStart(2, '0') + '/' + RoomCapacity;
    if (el.textContent !== want) el.textContent = want;
  }

  /* ---------- 输入（本地状态） ---------- */

  function readAct() {
    var c = document.querySelector('#app canvas');
    if (!c) return { greet: false, bow: 'idle', mode: state.game && validMode(state.game.mode) ? state.game.mode : 'walk' };
    return {
      greet: c.dataset.greeting === 'greeting',
      bow: state.game && state.game.self && state.game.self.isBowing ? 'repeating' : 'idle',
      mode: state.game && validMode(state.game.mode) ? state.game.mode : 'walk'
    };
  }

  function send(obj) {
    if (!state.ws || state.ws.readyState !== 1) return;
    try { state.ws.send(JSON.stringify(obj)); } catch (e) {}
  }

  function setGameConnection(connected) {
    var g = state.game;
    if (!g) return;
    if (state.id) g.selfId = state.id;
    if (typeof g.setVisitConnected === 'function') g.setVisitConnected(!!connected);
    applyIncenseSnapshot();
    applySeats();
  }

  function cushionPosition(index) {
    // Ss is scoped inside the bundled game, so positions are calculated by the
    // bundle callback. Here we keep the authoritative cushion ids only.
    return Number.isInteger(index) && index >= 0;
  }

  function applySeats() {
    var g = state.game;
    if (!g || !Array.isArray(g.occupiedCushions)) return;
    // The bundle's seat-request callback converts these ids through its Ss(i)
    // helper immediately before route calculation.
    g.__stOccupiedSeatIds = state.seats
      .filter(function (seat) { return seat && seat.id !== state.id && cushionPosition(seat.cushion); })
      .map(function (seat) { return seat.cushion; });
  }

  function applyIncenseSnapshot() {
    var g = state.game;
    if (!g) return;
    if (state.id) g.selfId = state.id;
    if (typeof g.setVisitConnected === 'function') g.setVisitConnected(state.connected);
    if (typeof g.setIncenseQueue === 'function') {
      g.setIncenseQueue({
        revision: state.incenseQueue.revision,
        members: state.incenseQueue.members.slice(),
        activeId: state.incenseQueue.activeId,
        expiresAt: state.incenseQueue.expiresAt,
        turnId: state.incenseTurnId
      });
    }
    if (typeof g.setIncense === 'function') g.setIncense(state.offerings || []);
  }

  function requestSeat(candidates) {
    if (!state.connected || !state.id) return Promise.resolve(candidates.length ? candidates[0] : null);
    if (state.seatRequest) return state.seatRequest.promise;
    var id = ++state.nextSeatRequestId;
    var resolve;
    var promise = new Promise(function (done) { resolve = done; });
    var request = { id: id, resolve: resolve, promise: promise, cancelled: false, timer: 0 };
    request.timer = setTimeout(function () {
      if (state.seatRequest === request) {
        state.seatRequest = null;
        resolve(null);
      }
    }, 6500);
    state.seatRequest = request;
    send({ t: 'seat-request', requestId: id, candidates: candidates });
    return promise;
  }

  function cancelSeatRequest() {
    var request = state.seatRequest;
    if (request) {
      request.cancelled = true;
      clearTimeout(request.timer);
      state.seatRequest = null;
      request.resolve(null);
    }
  }

  function finishSeat(arrived) {
    var leaseId = state.seatLeaseId;
    if (!arrived) cancelSeatRequest();
    if (!leaseId) return;
    if (arrived) send({ t: 'seat-arrived', leaseId: leaseId });
    else {
      send({ t: 'seat-release', leaseId: leaseId });
      state.seatLeaseId = '';
    }
  }

  function leaveIncense(abandon) {
    if (!state.connected) return false;
    state.incenseLeaveSerial++;
    send({ t: 'incense-leave', turnId: state.incenseTurnId || '', abandon: !!abandon });
    return true;
  }

  function incenseJoin() {
    if (!state.connected || !state.id) return Promise.resolve(false);
    if (state.incenseQueue.members.indexOf(state.id) >= 0) return Promise.resolve(true);
    var id = ++state.nextIncenseRequestId;
    return new Promise(function (resolve) {
      var timer = setTimeout(function () { delete state.incenseRequests[id]; state.expiredIncenseRequests[id] = 'join'; resolve(false); }, 6500);
      state.incenseRequests[id] = { resolve: resolve, timer: timer, kind: 'join', leaveSerial: state.incenseLeaveSerial };
      send({ t: 'incense-join', requestId: id });
    });
  }

  function incenseOffer() {
    if (!state.connected || !state.id || !state.incenseTurnId) return Promise.resolve(false);
    var id = ++state.nextIncenseRequestId;
    return new Promise(function (resolve) {
      var timer = setTimeout(function () { delete state.incenseRequests[id]; state.expiredIncenseRequests[id] = 'offer'; resolve(false); }, 6500);
      state.incenseRequests[id] = { resolve: resolve, timer: timer, kind: 'offer', leaveSerial: state.incenseLeaveSerial };
      send({ t: 'incense-offer', requestId: id, turnId: state.incenseTurnId });
    });
  }

  function incenseStart() {
    if (state.connected && state.id && state.incenseQueue.activeId === state.id && state.incenseTurnId) {
      send({ t: 'incense-start', turnId: state.incenseTurnId });
    }
  }

  function incenseNow() { return Date.now() + state.serverOffset; }

  function applyStarted(started) {
    var previous = state.incenseStarted;
    state.incenseStarted = started || null;
    if (!started || !started.id || !started.at || (previous && previous.id === started.id && previous.at === started.at)) return;
    var actor = state.peers[started.id];
    var g = state.game;
    if (!actor || !g || !actor.actorReady || typeof actor.offerIncense !== 'function') return;
    var age = Math.max(0, (incenseNow() - started.at) / 1000);
    // Match the game ritual's finite duration and replay its actual start stamp.
    if (age < 90) actor.offerIncense((g.time || 0) - age);
  }

  function applyIncenseState(m) {
    if (typeof m.serverTime === 'number') state.serverOffset = m.serverTime - Date.now();
    state.incenseTurnId = typeof m.turnId === 'string' ? m.turnId : '';
    var q = m.queue || {};
    state.incenseQueue = {
      revision: Number.isFinite(q.revision) ? q.revision : 0,
      members: Array.isArray(q.members) ? q.members.slice() : [],
      activeId: typeof q.activeId === 'string' ? q.activeId : null,
      expiresAt: Number.isFinite(q.expiresAt) ? q.expiresAt : 0
    };
    state.offerings = Array.isArray(m.offerings) ? m.offerings.slice() : [];
    applyIncenseSnapshot();
    applyStarted(m.started);
  }

  function updatePending(id, update) {
    for (var i = 0; i < state.pendingPeers.length; i++) {
      if (state.pendingPeers[i].id === id) Object.assign(state.pendingPeers[i], update);
    }
  }

  /* ---------- 连接 ---------- */

  function connect() {
    if (state.ws && (state.ws.readyState === 0 || state.ws.readyState === 1)) return;
    var ws;
    try { ws = new WebSocket(wsURL()); } catch (e) { scheduleRetry(); return; }
    state.ws = ws;

    ws.onopen = function () {
      state.connected = true;
      state.retry = 0;
      // 匿名访客标识：服务端也会给一个，这里带上去便于断线重连后保持同一身份
      // 服务端已在升级时入房并发送 welcome/join；客户端不重复加入。
      send({ t: 'pose', x: state.lastPose.x, z: state.lastPose.z, ry: state.lastPose.ry, mode: state.lastPose.mode });
      send({ t: 'act', greet: state.greeted, bow: state.bow });
      setGameConnection(true);
      clearInterval(state.heartbeat);
      state.heartbeat = setInterval(function () { send({ t: 'ping' }); }, 25000);
    };

    ws.onmessage = function (ev) {
      var m;
      try { m = JSON.parse(ev.data); } catch (e) { return; }
      if (m.t === 'welcome') {
        state.id = m.id;
        state.count = m.count;
        if (typeof m.serverTime === 'number') state.serverOffset = m.serverTime - Date.now();
        try { localStorage.setItem('st.visitor', m.id); } catch (e) {}
        Object.keys(state.peers).forEach(dropPeer);
        state.pendingPeers = (m.peers || []).slice();
        if (state.game) { state.pendingPeers.forEach(spawnPeer); state.pendingPeers = []; }
        setGameConnection(true);
        paintCount();
      } else if (m.t === 'seat-result') {
        var request = state.seatRequest;
        if (!request || request.id !== m.requestId) {
          if (m.leaseId) send({ t: 'seat-release', leaseId: m.leaseId });
          return;
        }
        clearTimeout(request.timer);
        state.seatLeaseId = m.leaseId || '';
        if (request.cancelled) {
          if (state.seatLeaseId) send({ t: 'seat-release', leaseId: state.seatLeaseId });
          state.seatLeaseId = '';
          state.seatRequest = null;
          request.resolve(null);
        } else {
          state.seatRequest = null;
          request.resolve(Number.isInteger(m.cushion) ? m.cushion : null);
          if (!Number.isInteger(m.cushion) && state.seatLeaseId) {
            send({ t: 'seat-release', leaseId: state.seatLeaseId });
            state.seatLeaseId = '';
          }
        }
      } else if (m.t === 'seats') {
        state.seats = Array.isArray(m.seats) ? m.seats.slice() : [];
        applySeats();
      } else if (m.t === 'incense-state') {
        applyIncenseState(m);
      } else if (m.t === 'incense-result') {
        var pending = state.incenseRequests[m.requestId];
        if (pending) {
          clearTimeout(pending.timer);
          delete state.incenseRequests[m.requestId];
          pending.resolve(!!m.ok);
          if (m.ok && pending.kind === 'join' && pending.leaveSerial !== state.incenseLeaveSerial) leaveIncense(true);
        } else if (m.ok && state.expiredIncenseRequests[m.requestId] === 'join') {
          delete state.expiredIncenseRequests[m.requestId];
          // Undo a late successful join so a cancelled ritual cannot strand a turn.
          leaveIncense(true);
        } else {
          delete state.expiredIncenseRequests[m.requestId];
        }
      } else if (m.t === 'join') {
        if (state.game) spawnPeer(m); else state.pendingPeers.push(m);
        if (typeof m.count === 'number') state.count = m.count;
        paintCount();
      } else if (m.t === 'pose') {
        var av = state.peers[m.id];
        var poseUpdate = { x: m.x, z: m.z, ry: m.ry };
        if (validMode(m.mode)) poseUpdate.mode = m.mode;
        if (av) {
          av.target = { x: m.x, z: m.z, ry: m.ry, mode: validMode(m.mode) ? m.mode : (av.target.mode || av.mode) };
          applyPeerMode(av, av.target.mode);
        } else updatePending(m.id, poseUpdate);
      } else if (m.t === 'act') {
        var av2 = state.peers[m.id];
        if (av2) syncPeerAct(av2, m.greet, m.bow, m.greetAt, m.bowAt);
        else updatePending(m.id, { greet: m.greet, bow: m.bow, greetAt: m.greetAt, bowAt: m.bowAt });
      } else if (m.t === 'leave') {
        dropPeer(m.id);
        state.pendingPeers = state.pendingPeers.filter(function (p) { return p.id !== m.id; });
        if (typeof m.count === 'number') state.count = m.count;
        paintCount();
      } else if (m.t === 'count') {
        state.count = m.n;
        paintCount();
      } else if (m.t === 'error') {
        state.count = -1;
        paintCount();
      }
    };

    ws.onclose = function () {
      state.connected = false;
      cancelSeatRequest();
      state.seatLeaseId = '';
      state.seats = [];
      Object.keys(state.incenseRequests).forEach(function (id) {
        var pending = state.incenseRequests[id];
        clearTimeout(pending.timer);
        pending.resolve(false);
        delete state.incenseRequests[id];
      });
      state.incenseQueue = { revision: 0, members: [], activeId: null, expiresAt: 0 };
      state.incenseTurnId = '';
      state.incenseStarted = null;
      state.offerings = [];
      setGameConnection(false);
      clearInterval(state.heartbeat);
      state.heartbeat = 0;
      state.count = -1;
      Object.keys(state.peers).forEach(dropPeer);
      state.pendingPeers = [];
      state.ws = null;
      paintCount();
      scheduleRetry();
    };
    ws.onerror = function () {};
  }

  function scheduleRetry() {
    var wait = Math.min(15000, 1000 * Math.pow(2, state.retry++));
    setTimeout(connect, wait);
  }

  window.addEventListener('online', function () { state.retry = 0; connect(); });
  document.addEventListener('visibilitychange', function () {
    if (!document.hidden) { state.retry = 0; connect(); }
  });

  /* ---------- 帧循环 ---------- */

  function hookRuntime() {
    var rt = window.sansaAppearanceRuntime;
    if (!rt || rt.__stLive) return !!rt;
    rt.__stLive = true;
    var orig = rt.update;
    var origDestroy = rt.destroy;
    rt.destroy = function (game) {
      destroyAmbient();
      state.dollsReady = false;
      Object.keys(state.peers).forEach(function (id) {
        var av = state.peers[id], t = av.target || {};
        state.pendingPeers.push({ id: id, x: t.x || 0, z: t.z || 0, ry: t.ry || 0, mode: av.mode,
          greet: av.greetWant, bow: av.bowWant ? 'repeating' : 'idle', greetAt: av.greetAt, bowAt: av.bowAt });
        dropPeer(id);
      });
      state.game = null;
      return origDestroy.call(this, game);
    };
    rt.update = function (game) {
      if (game && game.isReady && game.self && game.self.group) {
        var changed = state.game !== game;
        state.game = game;
        if (changed) setGameConnection(state.connected);
        // The legacy transport's setVisitors() resets selfId to its own empty
        // local identity. Reassert this adapter's identity on the game loop.
        if (state.id && game.selfId !== state.id) game.selfId = state.id;
        if (typeof game.setVisitConnected === 'function' && game.visitConnected !== state.connected) {
          game.setVisitConnected(state.connected);
        }
        if (state.pendingPeers.length) { state.pendingPeers.forEach(spawnPeer); state.pendingPeers = []; }
        if (!state.dollsReady) { spawnAmbient(game); state.dollsReady = true; }
      }
      return orig.call(this, game);
    };
    return true;
  }

  function tick() {
    var g = state.game;
    if (g && g.self && g.self.group) {
      var p = g.self.group.position;
      state.lastPose = { x: +p.x.toFixed(3), z: +p.z.toFixed(3), ry: +(g.self.group.rotation.y || 0).toFixed(3), mode: validMode(g.mode) ? g.mode : 'walk' };
    }
    var act = readAct();
    if (act.greet !== state.greeted || act.bow !== state.bow) {
      state.greeted = act.greet;
      state.bow = act.bow;
      send({ t: 'act', greet: act.greet, bow: act.bow });
    }
    var pose = state.lastPose, last = state.lastSentPose;
    var changed = !last || Math.abs(pose.x-last.x) > 0.008 || Math.abs(pose.z-last.z) > 0.008 || Math.abs(pose.ry-last.ry) > 0.015 || pose.mode !== last.mode;
    if (changed) {
      send({ t: 'pose', x: pose.x, z: pose.z, ry: pose.ry, mode: pose.mode });
      state.lastSentPose = { x: pose.x, z: pose.z, ry: pose.ry, mode: pose.mode };
    }
    ambientLabel();
    paintCount();
  }

  function frame() {
    var g = state.game;
    for (var id in state.peers) {
      var av = state.peers[id];
      var t = av.target;
      var now = g && g.time || 0;
      var dt = av.lastUpdateAt === undefined ? 0 : Math.max(0, Math.min(0.1, now - av.lastUpdateAt));
      var beforeX = av.group.position.x, beforeZ = av.group.position.z;
      if (t && dt > 0) {
        var blend = 1 - Math.exp(-10 * dt);
        av.group.position.x += (t.x - av.group.position.x) * blend;
        av.group.position.z += (t.z - av.group.position.z) * blend;
        if (g && typeof g.floorHeight === 'function') av.group.position.y = g.floorHeight(av.group.position.x, av.group.position.z);
        var d = t.ry - av.group.rotation.y;
        while (d > Math.PI) d -= Math.PI * 2;
        while (d < -Math.PI) d += Math.PI * 2;
        av.group.rotation.y += d * blend;
      }
      // 速度来自实际插值位移，按本地角色的步速归一化，避免原地摆腿或滑行。
      if (g && av.actorReady && dt > 0) {
        var moved = Math.hypot(av.group.position.x - beforeX, av.group.position.z - beforeZ);
        av.update(now, dt, Math.min(1, moved / dt / 1.85));
      }
      if (g) av.lastUpdateAt = now;
    }
    requestAnimationFrame(frame);
  }

  /* ---------- 启动 ---------- */

  var tries = 0;
  function boot() {
    if (!hookRuntime()) {
      if (tries++ < 120) return setTimeout(boot, 400);
    }
    connect();
    setInterval(tick, SEND_MS);
    requestAnimationFrame(frame);
  }

  window.ST = window.ST || {};
  window.ST.live = {
    state: function () {
      var ids = Object.keys(state.peers);
      return { room: state.room, id: state.id, connected: state.connected, count: state.count, peers: ids, seats: state.seats,
        incenseQueue: state.incenseQueue };
    },
    requestSeat: requestSeat,
    cancelSeatRequest: cancelSeatRequest,
    finishSeat: finishSeat,
    leaveIncense: leaveIncense,
    incenseJoin: incenseJoin,
    incenseOffer: incenseOffer,
    incenseStart: incenseStart,
    incenseNow: incenseNow,
    occupiedCushions: function () {
      return state.seats.filter(function (seat) { return seat && seat.id !== state.id && cushionPosition(seat.cushion); })
        .map(function (seat) { return seat.cushion; });
    }
  };

  boot();
})();
