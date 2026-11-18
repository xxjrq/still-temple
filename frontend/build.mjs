// 静寺前端构建：七语言页面由同一份 shell + 同一组模块生成。
// 产物写入 web/，并强制校验：.gz 解压后必须与原文件逐字节一致。
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const webDir = path.join(root, 'web');
const assetsDir = path.join(webDir, 'assets');

const cfg = JSON.parse(fs.readFileSync(path.join(here, 'bundle.json'), 'utf8'));
const shells = path.join(here, 'shell');
const srcDir = path.join(here, 'src');

function sha256(buf) {
  return crypto.createHash('sha256').update(buf).digest('hex');
}

function writeAsset(file, data) {
  const out = path.join(assetsDir, file);
  const bytes = Buffer.isBuffer(data) ? data : Buffer.from(data);
  fs.writeFileSync(out, bytes);
  const gz = zlib.gzipSync(bytes, { level: 9 });
  fs.writeFileSync(out + '.gz', gz);
  if (!zlib.gunzipSync(fs.readFileSync(out + '.gz')).equals(fs.readFileSync(out))) fail(`${file}.gz 与原文件不一致`);
  return { bytes: bytes.length, gzBytes: gz.length };
}

function fail(msg) {
  console.error('构建失败：' + msg);
  process.exit(1);
}

// 1. 前置校验：入口包必须指向配置里的主包，模块必须齐全
const baseEntryPath = path.join(assetsDir, cfg.entry);
const baseBundlePath = path.join(assetsDir, cfg.bundle);
if (!fs.existsSync(baseEntryPath)) fail(`入口包不存在 ${cfg.entry}`);
if (!fs.existsSync(baseBundlePath)) fail(`主包不存在 ${cfg.bundle}`);
if (!fs.readFileSync(baseEntryPath, 'utf8').includes(`assets/${cfg.bundle}`)) {
  fail(`入口 ${cfg.entry} 没有 import ${cfg.bundle}（历史上就是这里指错包，补丁全不生效）`);
}

const publicRuntimeAnchors = {
  "最近可达且空闲蒲团": "onSeatRequest:()=>St?.requestSeat()??Promise.resolve(null)",
  "无路可达时礼佛失败": "if(!r){this.cancelSeating(),this.clearBowRepetition(),this.cushion=null,this.callbacks.onStand(),this.callbacks.onSeatFailure();return}",
  "礼佛失败提示": "onSeatFailure:()=>ir(Jt===\"connected\"?\"빈 방석을 찾지 못했어요. 둘러보다 다시 눌러주세요.\":\"연결되면 방석을 찾을 수 있어요.\")"
};

function replaceOnce(source, before, after, name) {
  before = publicRuntimeAnchors[name] ?? before;
  const first = source.indexOf(before);
  if (first < 0 || source.indexOf(before, first + before.length) >= 0) fail(`${name} 基线片段不唯一，拒绝生成`);
  return source.slice(0, first) + after + source.slice(first + before.length);
}
let bundleSource = fs.readFileSync(baseBundlePath, 'utf8');
bundleSource = replaceOnce(bundleSource,
  'onSeatRequest:()=>Promise.resolve({cushion:0,x:Ss(0).x,z:Ss(0).z,yaw:Ss(0).yaw})',
  `onSeatRequest:()=>Promise.resolve((()=>{const g=wt;if(!g)return null;const p=g.self.group.position,taken=window.ST?.live?.occupiedCushions?.()||[],previous=g.occupiedCushions||[],last=g.__stNetworkSeatCoords||[],base=Array.isArray(g.__stBaseOccupiedCushions)?g.__stBaseOccupiedCushions:previous.filter(c=>!last.some(v=>Math.hypot(c.x-v.x,c.z-v.z)<.001));g.__stBaseOccupiedCushions=base.slice();const network=taken.map(i=>Ss(i));g.__stNetworkSeatCoords=network;g.occupiedCushions=[...base,...network];const order=Array.from({length:er},(_,i)=>i).filter(i=>!taken.includes(i)&&!base.some(c=>Math.hypot(c.x-Ss(i).x,c.z-Ss(i).z)<.08));order.sort((a,b)=>{const A=Ss(a),B=Ss(b);return Math.hypot(p.x-A.x,p.z-A.z)-Math.hypot(p.x-B.x,p.z-B.z)});const reachable=[];for(const i of order.slice(0,12))if(EE({x:p.x,z:p.z},i,g.movementObstacles))reachable.push(i);if(!reachable.length)for(const i of order.slice(12))if(EE({x:p.x,z:p.z},i,g.movementObstacles)){reachable.push(i);break}return window.ST?.live?.requestSeat?window.ST.live.requestSeat(reachable):Promise.resolve(reachable.length?reachable[0]:null)})())`, '最近可达且空闲蒲团');
bundleSource = replaceOnce(bundleSource, 'onStand:()=>St?.stand(),onSeating:wh',
  'onStand:()=>{window.ST?.live?.finishSeat?.(false),St?.stand()},onSeating:s=>{wh(s);if(!s){const g=wt,p=g?.self?.group?.position,i=g?.cushion,n=Number.isInteger(i)?Ss(i):null,arrived=!!(g&&g.mode==="kneel"&&p&&n&&Math.hypot(p.x-n.x,p.z-n.z)<.08);window.ST?.live?.finishSeat?.(arrived)}}',
  '到垫确认并释放蒲团租约');
bundleSource = replaceOnce(bundleSource, 'onIncenseOffer:()=>St?.offerIncense?.()??Promise.resolve(!1),onIncenseJoin:()=>St?.joinIncenseQueue?.()??Promise.resolve(!1),onIncenseLeave:s=>St?.leaveIncenseQueue?.(s),onIncenseStart:()=>St?.startIncenseRitual?.(),incenseNow:()=>St?.clockNow??Date.now()',
  'onIncenseOffer:()=>window.ST?.live?.incenseOffer?.()??Promise.resolve(!1),onIncenseJoin:()=>window.ST?.live?.incenseJoin?.()??Promise.resolve(!1),onIncenseLeave:s=>window.ST?.live?.leaveIncense?.(s),onIncenseStart:()=>window.ST?.live?.incenseStart?.(),incenseNow:()=>window.ST?.live?.incenseNow?.()??Date.now()',
  '香炉入口接入实时队列');
bundleSource = replaceOnce(bundleSource,
  'if(!r){this.cushion=i,this.seatJourney={seat:i,route:[],index:0,bow:e,stalled:0},this.setMode("kneel",!0),this.callbacks.onSeating(!0),this.callbacks.onMove(n.x,n.z,this.self.group.rotation.y,"kneel");return}',
  'if(!r){this.cancelSeating(),this.clearBowRepetition(),this.callbacks.onSeatFailure();return}', '无路可达时礼佛失败');
bundleSource = replaceOnce(bundleSource, "onSeatFailure:()=>ir(Jt===\"connected\"?__tr(\"빈 방석을 찾지 못했어요. 둘러보다 다시 눌러주세요.\",\"暂时没有空蒲团，请稍后再试。\",\"空いた座布団が見つかりません。散策してからお試しください。\",\"No free cushion right now. Look around and try again.\",\"Sin cojín libre ahora. Mira alrededor e inténtalo de nuevo.\",\"Pas de coussin libre. Regardez autour et réessayez.\",\"暫時沒有空蒲團，請稍後再試。\"):__tr(\"연결되면 방석을 찾을 수 있어요.\",\"连接后即可寻找蒲团。\",\"接続すると座布団を探せます。\",\"You can choose a cushion after connecting.\",\"Podrás elegir un cojín al conectar.\",\"Vous pourrez choisir un coussin après connexion.\",\"連接後即可尋找蒲團。\"))", "onSeatFailure:()=>ir(__tr(\"가까운 방석을 찾지 못했어요. 조금 더 가까이 이동한 뒤 다시 시도해 주세요.\",\"附近没有可到达的蒲团，请靠近一些后重试。\",\"近くに行ける座布団がありません。少し近づいてから再試行してください。\",\"No reachable cushion nearby. Move closer and try again.\",\"No hay un cojín accesible cerca. Acércate e inténtalo de nuevo.\",\"Aucun coussin accessible à proximité. Rapprochez-vous et réessayez.\",\"附近沒有可到達的蒲團，請靠近一些後重試。\"))", '礼佛失败提示');
bundleSource = replaceOnce(bundleSource, 'function QA(){const s=new _t;s.name="visitor";let e=jo({detail:"reduced",remote:!0})', 'window.sansaAppearanceRuntime.createAmbientActor=()=>jo({detail:"full"});function QA(){const s=new _t;s.name="visitor";let e=jo({detail:"reduced",remote:!0})', '共享默认香客角色工厂');
bundleSource = replaceOnce(bundleSource,
  'L.isTrusted&&L.detail>0&&performance.now()<f&&(L.preventDefault(),L.stopImmediatePropagation())',
  'L.isTrusted&&L.detail>0&&performance.now()<f&&(L.target===s||e.includes(L.target.closest("button")))&&(L.preventDefault(),L.stopImmediatePropagation())',
  '玻璃工具条只抑制原按钮的重复点击');
bundleSource = replaceOnce(bundleSource, 'updateBowRepetition(){if(this.visitConnected===!1)return;', 'updateBowRepetition(){', '礼佛重复动作');
bundleSource = replaceOnce(bundleSource, 'const g=p.x-n.x,m=p.z-n.z,v=Math.hypot(g,m),x=c.index===c.route.length-1?Math.min(1.85,v*4.5):1.85', 'const g=p.x-n.x,m=p.z-n.z,v=Math.hypot(g,m),x=c.index===c.route.length-1?Math.min(2.65,v*4.5):2.65', '礼佛路径步速');
bundleSource = replaceOnce(bundleSource, 'class k2{socket;visitToken;', 'class k2{localOnly=!1;socket;visitToken;', '本地传输字段');
bundleSource = replaceOnce(bundleSource, 'constructor(e,t,n={}){this.room=e.toLowerCase(),this.elastic=n.elastic??!0;', 'constructor(e,t,n={}){this.localOnly=!!n.localOnly,this.room=e.toLowerCase(),this.elastic=n.elastic??!0;', '本地传输设置');
bundleSource = replaceOnce(bundleSource, 'connect(){this.destroyed||this.full||this.blocked||this.suspended||this.socket?.readyState===WebSocket.OPEN||this.socket?.readyState===WebSocket.CONNECTING||', 'connect(){this.localOnly||this.destroyed||this.full||this.blocked||this.suspended||this.socket?.readyState===WebSocket.OPEN||this.socket?.readyState===WebSocket.CONNECTING||', '关闭旧实时传输');
bundleSource = replaceOnce(bundleSource, 'function wc(s=!1){clearTimeout(of);const e=matchMedia("(pointer: coarse)").matches;He("game-hint").textContent=Pr?"둘러보며 기다리거나 멈출 수 있어요"', 'function wc(s=!1){clearTimeout(of);const e=matchMedia("(pointer: coarse)").matches;He("game-hint").textContent=Pr?__tr("방석으로 이동 중 · 멈출 수 있어요","正在前往蒲团 · 可点击停止","座布団へ移動中 · タップで停止","Walking to a cushion · tap to stop","En camino al cojín · toca para parar","En route vers un coussin · touchez pour arrêter","正在前往蒲團 · 可點擊停止")', '礼佛移动提示');
bundleSource = replaceOnce(bundleSource, 'const n=Va?"멈추는 중":t?"멈추기":"절하기"', 'const n=Va?"멈추는 중":s?__tr("중지","停止","停止","Stop","Parar","Arrêter","停止"):t?"멈추기":"절하기"', '礼佛按钮移动反馈');
bundleSource = replaceOnce(bundleSource, 'new k2(Fg,Sl,{url:sb})', 'new k2(Fg,Sl,{url:sb,localOnly:!0})', '使用新实时传输');
// Avoid running the archived entry's side effects from the generated scene module.
bundleSource = replaceOnce(bundleSource, 'import{_ as Ug}from"./'+cfg.entry+'";', 'const Ug=loader=>loader();', 'public scene loader');
// The repository ships its existing archived scene runtime. Local UI additions share this locale selector.
bundleSource = 'const __tr=(...labels)=>{const indices={"ko":0,"zh":1,"zh-cn":1,"ja":2,"en":3,"es":4,"fr":5,"zh-hant":6};const lang=document.documentElement.lang.toLowerCase();return labels[indices[lang]??3]??labels[3]};\n' + bundleSource;
const bundleHash = sha256(Buffer.from(bundleSource));
const bundleFile = `${path.parse(cfg.bundle).name}-${bundleHash.slice(0, 12)}.js`;
const bundleSizes = writeAsset(bundleFile, bundleSource);

let entrySource = fs.readFileSync(baseEntryPath, 'utf8');
entrySource = replaceOnce(entrySource, `assets/${cfg.bundle}`, `assets/${bundleFile}`, '主包引用');
entrySource = replaceOnce(entrySource, `./${cfg.bundle}`, `./${bundleFile}`, '动态主包导入');
const entryHash = sha256(Buffer.from(entrySource));
const entryFile = `${path.parse(cfg.entry).name}-${entryHash.slice(0, 12)}.js`;
const entrySizes = writeAsset(entryFile, entrySource);
for (const m of cfg.modules) {
  if (!fs.existsSync(path.join(srcDir, m))) fail(`模块缺失 src/${m}`);
}

// 2. 先写入内容寻址模块，确保 HTML 落盘前引用的资源已存在。
const moduleManifest = {};
const moduleFiles = cfg.modules.map((m) => {
  const source = fs.readFileSync(path.join(srcDir, m));
  const hash = sha256(source);
  const file = `${path.parse(m).name}-${hash.slice(0, 12)}.js`;
  const sizes = writeAsset(file, source);
  moduleManifest[m] = { file, sha256: hash, ...sizes };
  return file;
});

// 3. 七语言页面只注入这一组共享模块。
const moduleTags = moduleFiles
  .map((m) => `<script defer src="/assets/${m}"></script>`)
  .join('');

// 4. 逐语言生成页面
const manifest = { builtAt: new Date().toISOString(), bundle: bundleFile, entry: entryFile, baseBundle: cfg.bundle, baseEntry: cfg.entry, modules: moduleManifest, pages: {} };
for (const page of cfg.pages) {
  const shellPath = path.join(shells, page);
  if (!fs.existsSync(shellPath)) fail(`shell 缺失 ${page}`);
  let html = fs.readFileSync(shellPath, 'utf8');

  if (!html.includes('<!-- {{MODULES}} -->')) fail(`${page} 缺少 {{MODULES}} 占位`);
  if (html.includes('mirror-patch')) fail(`${page} 仍带内联补丁，应改由模块注入`);

  html = html
    .replaceAll('<!-- {{MODULES}} -->', moduleTags)
    .replaceAll('{{BUNDLE}}', bundleFile)
    .replaceAll('{{ENTRY}}', entryFile);

  if (/\{\{.*?\}\}/.test(html)) fail(`${page} 有未替换的占位符`);

  const outPath = path.join(webDir, page);
  fs.writeFileSync(outPath, html);

  // 4. gzip + 一致性校验
  const gz = zlib.gzipSync(Buffer.from(html, 'utf8'), { level: 9 });
  fs.writeFileSync(outPath + '.gz', gz);
  const back = zlib.gunzipSync(fs.readFileSync(outPath + '.gz'));
  if (sha256(back) !== sha256(fs.readFileSync(outPath))) fail(`${page}.gz 与原文件不一致`);

  manifest.pages[page] = { sha256: sha256(Buffer.from(html)), bytes: Buffer.byteLength(html), gzBytes: gz.length };
}

manifest.bundleSha256 = bundleHash;
manifest.entrySha256 = entryHash;
manifest.bundleSizes = bundleSizes;
manifest.entrySizes = entrySizes;
fs.writeFileSync(path.join(here, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');

console.log(`构建完成：${cfg.pages.length} 页，bundle=${bundleFile}，release=${manifest.builtAt}`);
