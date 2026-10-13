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

**失败后的兜底链**只在客户端**真的能用**的方案里挑,顺序是 直连 → 代理 → 转封装 → 转码(便宜到贵):
iOS 上容器是 MKV 的片子不会被兜底到 302 直连(那条路必然播不了),而真重编码默认**不作为兜底**
(设置 → 播放 里的「失败后允许自动改用转码」可开)——小服务器追不上实时转码,切过去只会白吃 CPU。
播放器这边也会先分清楚"服务端还在生成分片"和"这个模式不对":前者重试同一方案,不急着换。

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

## 部署到 Linux(Debian / Ubuntu)

不想在服务器上装 Go 和 Node?直接下预编译包 —— 纯静态、无 CGO 依赖,Release 由
GitHub Actions 自动构建:

| 平台 | 架构 |
| --- | --- |
| linux | amd64 / arm64 / armv7 |
| windows | amd64 |

一键安装(Debian 12/13、Ubuntu 22.04/24.04 及衍生版):

```
curl -fsSL https://raw.githubusercontent.com/nekofinley/DoriyaMedia/main/scripts/install.sh | sudo bash
```

国内网络 `raw.githubusercontent.com` 常常打不开,那就用镜像把脚本取下来(脚本内部还会自动把
下载源回退到镜像,所以只要脚本本身能拉下来就行):

```
curl -fsSL https://ghfast.top/https://raw.githubusercontent.com/nekofinley/DoriyaMedia/main/scripts/install.sh | sudo bash
```

脚本会依次:安装 ffmpeg → 创建系统用户和数据目录 `/var/lib/doriyamedia` → 生成 `config.json`
→ 注册 systemd 服务并启动。下载 Release 时官方不通会**自动按
`官方 → ghfast.top → ghproxy.net → gh-proxy.com` 的顺序回退**,不用手动指定。
升级就是再跑一次同样的命令(自动覆盖二进制并重启);或者装好后直接用下面的 `dm` 命令。

常用选项:

```
# 指定版本
curl -fsSL <上面任一脚本地址> | sudo bash -s -- --version v0.1.0
# 换端口 / 数据目录 / 运行用户
sudo bash install.sh --port 8080 --data-dir /srv/doriyamedia --user media
# 固定用某个镜像源(不走自动回退)
sudo bash install.sh --base-url https://ghfast.top
# 只装不启动
sudo bash install.sh --no-start
# 卸载(--purge 连数据目录一起删)
sudo bash install.sh --uninstall
```

装完的日常操作,直接用安装附带的 **`dm` 管理命令**(类似宝塔的 `bt`,输入 `dm` 回车进菜单):

```
dm                  # 管理菜单:检查更新 / 更新 / 启停 / 日志 / 卸载
dm update           # 更新到最新版(dm update v0.2.1 可指定版本,也能用来降级)
dm check            # 只看看有没有新版本
dm status           # 版本 / 端口 / 运行状态
dm restart          # 启停(start / stop / restart)
dm log              # 跟随日志(Ctrl+C 退出)
dm uninstall        # 卸载(保留数据);dm purge 连数据一起删
```

不用 dm 的话,等价的 systemd 操作:

```
systemctl status doriyamedia
systemctl restart doriyamedia
journalctl -u doriyamedia -f        # 看日志
doriyamedia -version                # 看当前版本
```

也可以在服务器上自己编译(需要 Go 1.26+,`go.mod` 里写的就是 1.26.0;
前端产物 `internal/web/dist` 已随仓库提交,所以**不需要 Node**):

```
CGO_ENABLED=0 go build -ldflags "-s -w" -o doriyamedia ./cmd/doriyamedia
./doriyamedia                       # 前台运行,数据落在 ./data
```

> Linux 上的几个注意点:
> - `data/` 和 `config.json` 都相对**当前工作目录**,systemd 单元里已经设好了 `WorkingDirectory`;
>   手动跑的话记得在自己想要的目录里启动,否则数据会落在奇怪的地方;
> - ffmpeg / ffprobe 按 PATH 查找,也可以在配置里改成绝对路径;
> - **代理默认值是 Windows 上的本地代理**:Bangumi 的 `use_proxy` 默认 `true` 且指向
>   `socks5://127.0.0.1:7890`,在服务器上必然超时 —— 安装脚本会预置成直连。TMDB 默认直连。
>   国内网络两者都需要在 设置 → 刮削与播放 里勾选代理并填自己的地址;
> - 端口 5666 是非特权端口,服务以专用用户 `doriyamedia` 运行,不需要 root。

## 发布新版本

打 tag 即自动构建并发布 Release:

```
git tag v0.1.0 && git push origin v0.1.0
```

`.github/workflows/release.yml` 会:构建前端 → 交叉编译 4 个平台 → 打包 `tar.gz`/`zip`
→ 生成 `SHA256SUMS` → 创建 Release。版本号通过 `-ldflags "-X main.version=<tag>"` 注入。
`ci.yml` 在推送到 main 和 PR 时跑 `go vet` / `go test` / 编译校验。

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
| `GET /api/play/{fileId}?client=ios&force=auto` | 返回播放决策(模式、URL、字幕、音轨);首次探测未完成时立即返回 `state=probing` |
| `GET /api/play/{fileId}/status` | 探测进度:`idle / probing / ready / failed` + 已用时 + 失败原因 |
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

### 探测不再卡住起播(异步 + 预探测)

播放决策必须先探测源文件(容器/编码/音轨),而网盘文件探测很慢。所以:

- **`/api/play` 不再等探测**:没有缓存探测结果时,接口立即返回 `state=probing`
  (含标题与"正在探测媒体信息…"的原因),后台开始探测,前端轮询
  `/api/play/{id}/status`,就绪后再取一次决策。同一文件的探测是**单飞**的
  (多个页面/多次请求只探一次);失败会带原因返回,并冷却 30 秒避免反复打源。
- **探测窗口可配置**:默认 `probe_size_mb=8` + `probe_analyze_sec=4`(旧版固定 50MB/20s)。
  小窗口读不出来的文件会**自动用 50MB/20s 深度探测兜底**,所以不影响识别正确性。
- **扫描后后台预探测**:`preprobe_after_scan`(默认开)会在扫描结束后串行预热新文件的
  探测缓存(`preprobe_max_files` 限量、`preprobe_concurrency` 限并发),这样点开文件
  通常已经是 `ready`,直接出决策。

这几个开关都在 设置 → 刮削与播放 → 播放 里。

**关于「强制转封装」**:转封装只拷贝视频流,所以它只在**该客户端确实能解码这个视频编码**时才生效。
在 AV1 / VP9 / 10bit H.264 这类编码上强制转封装会自动退化为转码,并在播放页写明原因
(`手动指定转封装,但 AV1 无法被本机解码…已改为转码`)——否则会出现**只有声音、没有画面**:
视频流被原样拷进去了,但客户端根本解不开。

HLS 分片是**按需生成**的(每个片段请求时现场 `ffmpeg -ss`,转封装直拷),不写临时文件、
不预转整部片,seek 到哪生成到哪。播放 URL 都带 HMAC 签名,播放器无需带 Cookie。

### 字幕自动载入

字幕**不需要手动选**,打开就自动开。默认选哪一条由**服务端**决定(`/api/play` 里
`default_subtitle_id`,并在该条上标 `default:true`),规则是:中文优先 → 粤语 → 其他已知语言 →
无语言标记的;同语言内**简体优先于繁体**(两者 ISO 代码相同,只能靠标题区分,所以
`简日`/`繁体`/`chs`/`cht` 这类标记会被识别),再按文件内音轨顺序。内嵌字幕轨与同名字幕文件
都参与排序。

客户端拿到 `default` 后自动把该轨切到 `showing`;如果你想固定看某种语言,手动选一次即可,
选择会记在浏览器里(`dm:subPref`)并套用到后续文件。**手动关掉字幕也会被记住**——服务端标的
`default` 不会把它强行打开。

侧挂字幕按需转成 WebVTT(`.ass`/`.ssa` 会做基础转换,特效标签与定位会被丢弃),内嵌文本字幕轨
按需用 ffmpeg 抽取。图形字幕(PGS/VobSub)无法转成文本,不会出现在列表里。

## 界面(Web UI)

前端是 Vue3 + Vite 的单页应用,整体按流媒体网站的观感重做(参考 Netflix / Plex / MUBI 的暗色剧场风格):

- **全幅 Hero 剧场**:首页与详情页用背景大图 + 多层渐变遮罩,标题自动轮播、鼠标悬停暂停;
  背景有极缓的 ken-burns 位移,不会静止得像一张图。
- **横向影轨**:继续观看(带进度条与「还剩 x 分钟」)、最近添加的电影/剧集、评分最高的 Top 10
  (超大序号叠在封面上);影轨支持吸附滚动、悬停出现左右箭头。
- **海报卡**:悬停放大 + 信息浮层 + 快捷播放按钮;没有海报时用标题生成的渐变占位图,不留灰块。
- **顶栏与移动端**:顶栏在 Hero 上是透明渐变、滚动后变毛玻璃;`⌘/Ctrl + K` 呼出跨媒体库搜索面板;
  窄屏(<900px)自动切换为底部标签栏,并适配 iPhone 安全区。
- **播放页(三种形态,按设备自动选)**——都共用同一个 `<video>` 与同一条决策链路,
  `?view=phone|tablet|theater` 可以强制指定:
  - **桌面 / 手机横屏 = 全屏剧场**:自定义控制条(播放/暂停、±10s、音量、进度拖动与缓冲显示、
    倍速、画中画、全屏、快捷键 `空格/←→/J K/↑↓/M/F/P/0-9`),右侧抽屉切换版本/音轨/字幕/
    播放速度/播放模式;
  - **手机竖屏 = 竖屏观看页**(YouTube / Bilibili 那种):视频 16:9 在顶部,控制条紧贴视频下方,
    再往下是标题与元信息、**上一集 / 下一集**、`选集 / 简介 / 设置` 三个标签页——选集是数字网格
    (当前集高亮),设置是内嵌的音轨/字幕/倍速/播放模式;点选集会自动滚回顶部,
    转成横屏立刻变回全屏剧场且播放不中断;
  - **iPad / 平板**:视频在左上角、控制条在视频下面,**选集做成右侧侧栏**(带剧照、当前集高亮、
    剧集标题与播出日期),标签页只剩 `简介 / 设置`;电影之类没有剧集的条目则收起侧栏、视频居中。
  - 选集列表来自 `条目 → 季 → 该季全部剧集`,只在上面两种形态里拉取;剧集之间切换用
    `router.replace`,所以返回键仍然是回到季页面而不是逐集后退。
  - iOS 无论哪种形态都用**原生播放控件**以保证兼容性;准备阶段显示**分片生成进度条**与
    **编码速度 x× 实时**(低于 0.9× 时提示降低转码高度)。
- **播放手势**:触摸设备上**单击画面不暂停**(手搭在屏幕上太容易误触),单击只把控制条唤回来,
  暂停/播放要点**双击**(自己数 240ms 内的两下,不用浏览器的 dblclick——它的判定窗口太宽,
  会把两次不相干的点击算成双击);桌面端保持「单击暂停 / 双击全屏」。iOS 交给原生控件。
- **全屏锁定**:进入全屏后画面**左侧正中**有一个锁按钮,点一下锁定——顶部返回、控制条、进度条、
  快捷键提示全部隐藏,并且不再接收触摸(隐藏按钮的位置也不再是按钮,点下去只会落到视频上被忽略),
  再点一下解锁;退出全屏自动解锁。桌面端的键盘快捷键不受锁定影响,`Esc` 始终能退出全屏。
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
