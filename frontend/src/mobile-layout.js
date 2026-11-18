(function () {
  'use strict';

  const style = document.createElement('style');
  style.textContent = `
.game-tools.st-unified-tools .liquid-nav-source {
  align-items: center;
  gap: 2px;
}
.game-tools.st-unified-tools .temple-controls {
  position: static;
  inset: auto;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0;
  background: transparent;
}
.game-tools.st-unified-tools .temple-controls .about-pill,
.game-tools.st-unified-tools .temple-controls .lang-toggle {
  height: 44px;
  min-width: 44px;
  pointer-events: auto;
  justify-content: center;
  padding: 0 10px;
  border: 0;
  background: transparent;
  box-shadow: none;
}
.game-tools.st-unified-tools .temple-controls .about-pill:hover,
.game-tools.st-unified-tools .temple-controls .lang-toggle:hover,
.game-tools.st-unified-tools .temple-controls .about-pill:focus-visible,
.game-tools.st-unified-tools .temple-controls .lang-toggle:focus-visible {
  background: #e0c49c22;
}
.game-tools.st-unified-tools .temple-controls .lang-list {
  top: calc(100% + 8px);
  right: 0;
  pointer-events: auto;
}
.game-header:has(.st-unified-tools .lang-list:not([hidden])) {
  z-index: 6;
}
@media (max-width: 700px) {
  .game-header .game-entry-tools {
    width: 100%;
    min-width: 0;
  }
  .game-header .st-unified-tools {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
  }
  .game-context {
    top: calc(max(14px, env(safe-area-inset-top, 0px)) + 116px);
  }
  .game-context-actions {
    align-self: flex-end;
    gap: 6px;
    max-width: 100%;
  }
  .game-context-actions .chant-invite {
    min-height: 42px;
    padding: 7px 12px;
  }
  .game-bottom {
    bottom: calc(env(safe-area-inset-bottom, 0px) + 14px);
    width: min(420px, calc(100% - 24px));
  }
}
@media (max-width: 700px) and (max-height: 520px) {
  .game-bottom {
    bottom: calc(env(safe-area-inset-bottom, 0px) + 8px);
  }
}
`;
  document.head.appendChild(style);

  const templeControls = document.querySelector('.temple-controls');
  const gameRoot = document.getElementById('app');
  if (!templeControls || !gameRoot) return;

  const observer = new MutationObserver(mountTools);
  function mountTools() {
    // Wait for the renderer to finish wrapping the original audio and settings buttons.
    const source = gameRoot.querySelector('.game-tools .liquid-nav-source');
    if (!source) return;
    observer.disconnect();
    source.prepend(templeControls);
    source.parentElement.classList.add('st-unified-tools');
  }
  observer.observe(gameRoot, { childList: true, subtree: true });
  mountTools();
})();
