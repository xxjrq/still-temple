# 素材来源与署名

本仓库的 3D 模型、音频与前端代码并非本项目原创。以下逐项说明来源、许可和我们的改动，以便转载时保留同样的署名。

## 1. 佛像模型

文件：`web/optimized/buddha-*.glb`、`avalokitesvara-*.glb`、`mahastamaprapta-*.glb`、`monk-*.glb`、`pine-*.glb`

- 原型：韩国国立中央博物馆藏「鎏金铜佛坐像·菩萨立像」（德寿 2116）
- 许可：**韩国公共资料第 1 类（KOGL Type 1，공공누리 제1유형）**
- 许可范围：允许商用、允许修改，须注明出处
- 我们的改动：为网页调整了尺寸、贴图与压缩；两侧胁侍在姿态与冠饰上做了调整，构成观音与大势至菩萨，这一组合是原作的现代诠释

## 2. 音频

- `web/audio/moktak-hit-natural.wav`：**「목탁2」 · 김용배 · 한국저작권위원회 공유마당 · CC BY 4.0**（原页 <https://gongu.copyright.or.kr/gongu/wrt/wrt/view.do?menuNo=200020&wrtSn=13253416>）。改动：单次敲击截取、转单声道、音量归一化、首尾淡入淡出。
- `web/audio/temple-chant-review-v1.mp3`：**「염불소리1」 · 김용배 · 한국저작권위원회 공유마당 · CC BY 4.0**（原页 <https://gongu.copyright.or.kr/gongu/wrt/wrt/view.do?menuNo=200020&wrtSn=13254274>）。改动：整段保留、转 mono MP3 112kbps/48kHz、2-pass loudnorm 至约 -20 LUFS。**此文件是原站标注的“诵读试听/审查版”，未确认是完整《般若心经》读诵，站内不得表述为完整心经或正式法会实录。**
- `web/audio/moonlight-yard-v2.mp3`：原站自带素材，随仓库一并提供，许可依原站说明。
- 我们的改动：截取片段、调整音量与压缩格式
- 许可全文：<https://creativecommons.org/licenses/by/4.0/>

## 3. 前端代码

`web/` 下的 HTML、JS、CSS 来自原站公开页面的学习性镜像。代码版权归原作者所有。

我们的改动仅限：品牌名称替换（Sansa → 静寺 / Still Temple）、页面标题与描述调整、去掉捐赠入口。未声称为原创作品。

## 4. 字体

`web/fonts/SansaQuietUI-Core.woff2` 随原站分发，许可状态未公开。若权利人提出异议，会在 48 小时内移除并替换为开源字体。

## 5. 本项目原创部分

`main.go`、`rooms.go`、`site.go`、`main_test.go`、构建与部署脚本、README 与本文档，采用 MIT 许可。

## 联系

如认为以上任一项超出了许可范围，请在本仓库开 issue，或邮件联系仓库维护者。
