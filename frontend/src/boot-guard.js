/* 首屏守卫：只做两件不打扰的事。
   1) 设备没有 WebGL 时给一行说明（3D 主包自己会降级成「先拨念珠／继续休息」，不覆盖它的界面）。
   2) 3D 迟迟没接管时，说明还在下载，并给一张静态院子图，别让人一遍遍点重试。 */
(function () {
  function webglOK() {
    try {
      var c = document.createElement('canvas');
      return !!(c.getContext('webgl2') || c.getContext('webgl') || c.getContext('experimental-webgl'));
    } catch (e) {
      return false;
    }
  }

  function isZh() {
    return /^zh/.test((document.documentElement.lang || '').toLowerCase());
  }

  function note(text) {
    if (document.getElementById('boot-guard-note')) return;
    var el = document.createElement('div');
    el.id = 'boot-guard-note';
    el.textContent = text;
    el.style.cssText = 'position:fixed;left:50%;transform:translateX(-50%);bottom:76px;z-index:40;' +
      'max-width:min(92vw,560px);padding:10px 14px;border-radius:10px;' +
      'background:rgba(32,28,24,.92);color:#efe7d8;font:13px/1.6 system-ui,"Songti SC",serif;text-align:center';
    document.body.appendChild(el);
    setTimeout(function () { el.style.opacity = '0'; el.style.transition = 'opacity .6s'; }, 9000);
    setTimeout(function () { el.remove(); }, 9800);
  }

  function init() {
    var hasWebgl = webglOK();

    if (!hasWebgl) {
      note(isZh()
        ? '这台设备没有启用 3D 渲染（WebGL），佛堂画面出不来；念珠、休息等本地功能不受影响。'
        : 'This device has no WebGL, so the 3D hall cannot render. Local features such as beads and resting still work.');
    }

    var boot = document.getElementById('sansa-boot');
    if (!boot) return;

    /* 弱网首次进入需要更久：给真实说明 + 静态院子，保留重新加载 */
    var timer = setTimeout(function () {
      if (!document.getElementById('sansa-boot')) return;
      var m = document.getElementById('sansa-boot-message');
      if (m) {
        m.textContent = isZh()
          ? '3D 佛堂还在加载，首次进入需要多等一会儿。网络较慢时可以先看这处院子，也可以点下面重新加载。'
          : 'The 3D hall is still loading; the first visit takes longer. You can look at this courtyard meanwhile, or reload below.';
      }
      var dot = boot.querySelector('.boot-dot');
      if (dot) dot.style.display = 'none';
      if (!boot.querySelector('.temple-still') && document.body) {
        var st = document.createElement('style');
        st.id = 'boot-guard-style';
        st.textContent = '.temple-boot img.temple-still{position:absolute;inset:0;width:100%;height:100%;object-fit:cover;opacity:.45;pointer-events:none}';
        document.head.appendChild(st);
        var img = document.createElement('img');
        img.className = 'temple-still';
        img.src = '/assets/temple-still.jpg';
        img.alt = '';
        img.decoding = 'async';
        boot.insertBefore(img, boot.firstChild);
      }
    }, 15000);

    var clear = function () {
      if (document.getElementById('sansa-boot')) return;
      clearTimeout(timer);
      if (mo) mo.disconnect();
    };
    var mo = typeof MutationObserver === 'function' ? new MutationObserver(clear) : null;
    if (mo) mo.observe(document.body, { childList: true, subtree: true });
    window.addEventListener('load', function () { setTimeout(clear, 800); });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
  } else {
    init();
  }
})();
