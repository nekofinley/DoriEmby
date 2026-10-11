# DoriyaMedia

一个用 Go 写的轻量 Emby 替代品:挂载网盘(OpenList / WebDAV / 本地目录)、扫描媒体库、TMDB 刮削,
并且**针对 iOS Safari 做了播放决策引擎** —— 能直连的一律 302 直连(不占服务器带宽),iOS 播不了的
(MKV / hev1 / EAC3 等)自动转封装成 HLS,视频流直拷、CPU 几乎零消耗。

## 解决什么问题

用 OpenList 挂载天翼云盘时,PC 网页可以直接 302 播放,但 iPhone/iPad 上不行 —— 根因是 iOS
Safari 的解码限制:

| 情况 | iOS Safari 能否直接播 |
| --- | --- |
| MP4 + H.264 + AAC | ✅ 可以 |
| MP4 + HEVC(`hvc1` 标签) + AAC | ✅ 可以(硬解) |
| MP4 + HEVC(`hev1` 标签) | ❌ Safari 要求 `hvc1`,黑屏 |
| MKV(任意编码) | ❌ Safari 根本不支持 MKV 容器 |
| EAC3 / DTS / TrueHD 音轨 | ❌ 有画面没声音 |
| 杜比视界 Profile 5 | ❌ 绿紫色花屏 |

DoriyaMedia 的处理方式:

1. 播放前用 ffprobe(对网盘文件走 HTTP Range 远程探测,结果缓存)拿到容器/编码/编码标签/音轨信息;
2. 结合 UA 识别客户端能力(iOS Safari / Chrome 系 / Firefox);
3. **决策**:
   - 客户端原生支持 + 源可出直链(OpenList/网盘)→ **HTTP 302 直连**,流量不经过服务器;
   - 客户端原生支持但源不可直连(本地目录 / WebDAV)→ **代理播放**(支持 Range);
   - 视频可拷但容器/标签/音轨不兼容 → **HLS 转封装**(`-c:v copy`,只把不兼容音轨转成 AAC),
     iOS Safari 用 AVPlayer 原生播 HLS,HEVC 走硬解;
   - 视频编码本身播不了(RMVB/AV1 on 老设备等)→ **HLS 转码**(x264 veryfast,>1080p 自动降级);
4. 播放页可以随时手动覆盖决策(强制直连 / 转封装 / 转码)。

## 快速开始

```
scripts\build.ps1              # 构建前端 + 嵌入,产出 doriyamedia.exe
.\doriyamedia.exe              # 默认监听 0.0.0.0:5666,数据目录 ./data
```

> 项目从 `Doriya` 改名为 `DoriyaMedia`(模块 `doriyamedia`,二进制 `doriyamedia.exe`)。
> 数据库也改名为 `data/doriyamedia.db`:**首次启动会自动把旧的 `data/doriya.db`(含 WAL 文件)
> 改名过来**,媒体库、扫描结果、账号都原样保留,不需要重扫;`config.json` 文件名不变。

浏览器打开 `http://<服务器IP>:5666`,首次进入会要求创建管理员账号。然后:

1. **设置 → 存储源**:添加 OpenList 源(见下文);
2. **设置 → 媒体库**:选源、填路径(如 `/movies`)、选类型和刮削来源,创建;
3. 点该媒体库的 **扫描**,等状态变为 `done`;
4. 首页出现海报,点进详情 → 播放。用 iPhone Safari 打开同一个地址即可直接播放。

**不想刮削/不想整理结构?** 直接用顶部导航的 **「文件」**:浏览任意存储源的目录,点视频就播
(同样经过决策引擎:该 302 直连就 302,iOS 播不了的自动转封装,并支持断点续播)。
也可以只建库不填 TMDB key —— 扫描后照样能播,只是没有海报和简介。

环境变量:`DORIYAMEDIA_LOCAL=1` 只监听 127.0.0.1。

## 接入 OpenList(天翼云盘)

1. 部署 [OpenList](https://github.com/OpenListTeam/OpenList),添加 **天翼云盘** 存储并填入账号
   (天翼云盘新版需要验证码/设备指纹,按 OpenList 文档操作即可),确认 OpenList 网页里能播放/下载;
2. 在 OpenList 后台 `设置 → 其他` 复制 **令牌(token)**;
3. DoriyaMedia → 设置 → 存储源 → 类型选 `OpenList / AList`:
   - 地址:`http://<openlist主机>:5244`
   - Token:粘贴上面的令牌(不填则要求该路径允许游客访问);
4. 建媒体库 → 扫描。

### 直连(302)的注意点

- 网盘直链一般**绑定出口 IP 和时效**。家里宽带下,服务器和手机走同一个公网出口,302 直连没问题;
  如果手机在外网(流量)、服务器在家,直链可能 403 —— 播放器里点 **强制转封装** 即可
  (转封装模式下由服务器拉流,IP 与取链时一致,总是可用)。
- 若 OpenList 对该存储设置了"代理",`fs/get` 返回的是 OpenList 自己的 `/p` 链接,同样能 302。

### WebDAV 源

WebDAV 源的直链通常需要鉴权头,浏览器拿不到,所以 DoriyaMedia 对 WebDAV 一律走服务器代理
(支持 Range 拖动)。想白嫖 302 直连,请用 OpenList 源。

## 剧集识别与季/版本结构

扫描按 **剧名 → 季 → 集** 归组,目录怎么套都能认:

- 季目录:`第 1 季` / `第2季` / `第二季` / `Season 1` / `S01` / 纯数字(`1`、`0`);
- 版本目录不参与剧名:`bdrip` / `webrip` / `1080p` / `x265` / 压制组目录…… 会被跳过,
  所以 `剧名/bdrip/第 1 季/…` 和 `剧名/第 1 季/webrip/…` 都归到同一个剧的同一季;
- **特别篇独立成季**:`OAD` / `OVA` / `剧场版` / `SP` / `特典` / `番外` / `总集篇` 各自一张季卡片,
  不会再出现"剧名就叫 OAD / bdrip"的情况;
- **子系列 / 篇章目录算"命名季"**:`物语系列/{化物语,伪物语}`、`青之驱魔师/{岛根启明结社篇,京都不淨王篇}`、
  `刀剑神域/{Alicization篇,…}` 这类"父目录下挂着好几部子系列"的结构,会把**父目录当剧名**、
  每个子目录当一季(季卡片直接显示 `化物语` / `岛根启明结社篇`),不会再分裂成好几部同名剧;
  命名季的季号是自动分配的,不会和 `第 1 季` 抢号;
- **分类目录与字幕目录不会变成剧名**:`Movie` / `Anime` / `合集` / `未分类` 这类归类目录,
  以及 `简体` / `CHS` / `Big5` 这类语言目录都会被跳过(`/Cartoon/Movie/你的名字/…` 的剧名是 `你的名字`);
- **同一集的多版本会自动合并**:同一季里 bdrip 与 webrip 的同编号文件落进同一集,
  剧集页先选季 → 季页面每集右侧选 `BDRip · HEVC 10bit · 1080p` / `WebRip · AVC · 1080p` 再播,
  播放页底部也能随时切版本;**单版本剧集**同样有播放按钮(接口对每集都会给出可播文件);
- 重扫会**重新归组**已有文件(文件行按源+路径唯一),改了解析规则不需要删库重建;
  重扫后空的旧剧/旧季会被自动清理,重复条目(如两个"物语系列")合并成一个。

## 刮削

- 来源按优先级:**本地 nfo/海报 → TMDB**(每个媒体库可配置,勾选即启用);
- 本地 nfo 支持 Kodi 格式:`movie.nfo` / `tvshow.nfo` / `*.nfo`、`poster.jpg`、`fanart.jpg` 等
  (支持网盘目录里直接读取);
- TMDB 需要在 设置 → 刮削与播放 里填 API Key(免费,v3 密钥与 v4 读访问令牌都支持);
- 国内网络两种走法:**① 勾选「走本地代理访问 TMDB」**并填代理地址(默认 `socks5://127.0.0.1:7890`,
  也支持 `http://`),TMDB 接口与海报/背景图下载都会走这个代理;② 把 API/图片地址改成镜像或自建反代。
  设置页有 **测试连接** 按钮,会实时区分「代理没启动 / 超时被墙 / Key 无效」;
- 详情页可对单个条目 **重新识别**;番剧名有歧义时,在详情页填 **Bangumi ID** 点「按 ID 匹配」即可钉住某一部。

### Bangumi(bgm.tv)—— 番剧中文数据

番剧库建议勾上 **Bangumi** 这个刮削来源,它补齐 TMDB 的短板:

- **中文剧名**(原名进 `original_title`,如「孤独摇滚！」/「ぼっち・ざ・ろっく！」);
- **每集中文标题 + 播出日期**(如「第 1 集 翻转孤独 · 2022-10-08」);
- 评分、标签(芳文社 / 音乐 / 轻百合…)、封面。

配置在 设置 → 刮削与播放 → **Bangumi 刮削**:令牌可留空(公开接口够用),
**网络代理默认开启** —— `api.bgm.tv` 在不少网络下会被 DNS 污染(解析到无关 IP 后超时),
走本地代理即正常;有 **测试连接** 按钮,会返回中文/日文搜索结果,一眼看出是网络问题还是没这个条目。

## 播放 / API

| 端点 | 说明 |
| --- | --- |
| `GET /api/play/{fileId}?client=ios&force=auto` | 返回播放决策(模式、URL、字幕、音轨) |
| `GET /api/browse/{sourceId}?path=/` | 文件浏览器:列出存储源目录(免建库免扫描) |
| `GET /api/browseplay/{sourceId}?path=...` | 浏览器里点开即播(注册到隐藏库,完整走决策管线) |
| `PUT /api/libraries/{id}` | 编辑媒体库(改名/类型/**路径**/刮削源) |
| `POST /api/libraries/{id}/scan` | 触发扫描;`GET /api/scan/status` 实时返回阶段/目录/文件数/错误 |
| `POST /api/libraries/{id}/scan/stop` | 中止正在进行的扫描 |
| `GET /api/stream/{fileId}` | 302 直连或 Range 代理(带签名) |
| `GET /api/hls/{fileId}/index.m3u8` | 按需生成的 HLS(VOD 播放列表 + 每片段实时 ffmpeg) |
| `GET /api/subs/{fileId}/{subId}.vtt` | 外挂/内嵌字幕转 WebVTT |
| `GET /api/img/{itemId}/{poster\|backdrop\|still}` | 海报/背景图 |
| `POST /api/metadata/test` | 用当前/表单里的设置真实请求一次 TMDB 或 Bangumi(`"provider":"bgm"`),测代理/Key/连通性 |
| `POST /api/items/{id}/refresh` | 重新识别;可带 `{"bgm_id":328609}` 手动钉住 Bangumi 条目 |

扫描全程可见:遍历阶段实时显示"目录数/文件数/当前路径",整理阶段按文件计数,
识别刮削阶段按条目计数;读取失败的目录会计数并给出首个错误;扫描完成后保留
"上次结果",失败会以红色展示原因(如 OpenList 路径不存在 `object not found`)。

HLS 分片是**按需生成**的(每个片段请求时现场 `ffmpeg -ss`,转封装直拷),不写临时文件、
不预转整部片,seek 到哪生成到哪。播放 URL 都带 HMAC 签名,播放器无需带 Cookie。

## 界面(Web UI)

前端是 Vue3 + Vite 的单页应用,整体按流媒体网站的观感重做(参考 Netflix / Plex / MUBI 的暗色剧场风格):

- **全幅 Hero 剧场**:首页与详情页用背景大图 + 多层渐变遮罩,标题自动轮播、鼠标悬停暂停;
  背景有极缓的 ken-burns 位移,不会静止得像一张图。
- **横向影轨**:继续观看(带进度条与「还剩 x 分钟」)、最近添加的电影/剧集、评分最高的 Top 10
  (超大序号叠在封面上);影轨支持吸附滚动、悬停出现左右箭头。
- **海报卡**:悬停放大 + 信息浮层 + 快捷播放按钮;没有海报时用标题生成的渐变占位图,不留灰块。
- **顶栏与移动端**:顶栏在 Hero 上是透明渐变、滚动后变毛玻璃;`⌘/Ctrl + K` 呼出跨媒体库搜索面板;
  窄屏(<900px)自动切换为底部标签栏,并适配 iPhone 安全区。
- **播放页**:自定义控制条(播放/暂停、±10s、音量、进度拖动与缓冲显示、倍速、画中画、全屏、
  快捷键 `空格/←→/J K/↑↓/M/F/P/0-9`),侧边抽屉切换版本/音轨/字幕;iOS 仍用原生播放控件以保证兼容性。
- **细节**:骨架屏、图片渐入、Toast、空状态、焦点环、`prefers-reduced-motion` 降级、列表/网格视图切换。

设计 token 集中在 `webui/src/styles.css`,组件在 `webui/src/components/`。

## 开发

```
webui\        Vue3 + Vite 前端(pnpm dev 热更新,代理 /api → 5666)
  src/components/  顶栏、影轨、海报卡、Hero、搜索面板、Toast、图标…
  src/views/       首页 / 媒体库 / 详情 / 季集 / 文件 / 播放 / 设置 / 登录
cmd\doriyamedia\   服务端入口
cmd\mockopenlist\  OpenList v3 API 的本地 mock(测试 302 链路用)
scripts\e2e-test.ps1   端到端冒烟测试(先启动 doriyamedia + mockopenlist + testdata)
scripts\browse-live-test.ps1   对真实 OpenList 跑浏览→决策→302/转封装链路
testdata\     ffmpeg 生成的测试片源(h264/hvc1/hev1/mkv-eac3/剧集+字幕)
```

调试前端:`pnpm dev`(端口 5166)+ 后端 `.\doriyamedia.exe`,浏览器开 http://127.0.0.1:5166。
也可以给后端配置 `dev_web_dir`(例如 `internal/web/dist`),这样改完前端只需 `pnpm build`、不用重启后端。

> 本地 NFO/剧照配对修了一处 bug:此前只按「文件名含扩展名」查找(`xx.mkv.nfo`),
> Kodi 约定的 `xx.nfo` / `xx-thumb.jpg` 永远匹配不上,导致剧集没有剧照和单集标题;
> 现在两种命名都能识别(`internal/metadata/manager.go`)。

## 已知限制 / Roadmap

- [ ] iOS Safari 原生 HLS 播放时,外挂 VTT 字幕的 `<track>` 支持不稳定(建议用内嵌文字字幕,或等 HLS 字幕轨方案);
- [ ] PGS/图形字幕只在转码模式下有效(烧录),暂不支持外挂;
- [ ] 多音轨在 302 直连模式下由浏览器选择(通常第一条),HLS 模式下可切换;
- [ ] 剧集季/集刮削依赖 Show 已匹配 TMDB;手动指定 TMDB ID 在详情页"重新识别"里;
- [ ] 多用户/权限、播放队列、WebDAV 直链透传、天翼云盘原生驱动(免 OpenList)在计划中。
