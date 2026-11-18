/* 本地合掌解锁：
   原版把 #hello（合掌）绑在 WebSocket connected 状态上，WS 不可用时永远禁用。
   现在 3D 场景就绪后即解锁，动作本身是本地动画，断网也能点。
   正在合掌（aria-busy=true / is-active）时保持禁用，不干扰原状态机。 */
(function () {
  function sceneReady() {
    return !!document.querySelector('#app canvas') &&
      !document.getElementById('sansa-boot');
  }

  function unlock() {
    var btn = document.getElementById('hello');
    if (!btn) return;
    if (btn.getAttribute('aria-busy') === 'true') return;
    if (btn.classList.contains('is-active')) return;
    if (btn.disabled) {
      btn.disabled = false;
      btn.removeAttribute('disabled');
    }
    if (!btn.dataset.localGreet) {
      btn.dataset.localGreet = '1';
      var title = btn.getAttribute('title') || '合掌';
      btn.setAttribute('title', title + '（本地动作，不依赖联网）');
    }
  }

  var ticks = 0;
  var timer = setInterval(function () {
    ticks += 1;
    if (sceneReady()) unlock();
    if (ticks > 360) clearInterval(timer);
  }, 400);

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { setTimeout(unlock, 2000); }, { once: true });
  } else {
    setTimeout(unlock, 2000);
  }
})();
