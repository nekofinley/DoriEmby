# Doriya

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

Doriya 的处理方式:

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
scripts\build.ps1              # 构建前端 + 嵌入,产出 doriya.exe
.\doriya.exe                   # 默认监听 0.0.0.0:5666,数据目录 ./data
```

浏览器打开 `http://<服务器IP>:5666`,首次进入会要求创建管理员账号。然后:

1. **设置 → 存储源**:添加 OpenList 源(见下文);
2. **设置 → 媒体库**:选源、填路径(如 `/movies`)、选类型和刮削来源,创建;
3. 点该媒体库的 **扫描**,等状态变为 `done`;
4. 首页出现海报,点进详情 → 播放。用 iPhone Safari 打开同一个地址即可直接播放。

环境变量:`DORIYA_LOCAL=1` 只监听 127.0.0.1。

## 接入 OpenList(天翼云盘)

1. 部署 [OpenList](https://github.com/OpenListTeam/OpenList),添加 **天翼云盘** 存储并填入账号
   (天翼云盘新版需要验证码/设备指纹,按 OpenList 文档操作即可),确认 OpenList 网页里能播放/下载;
2. 在 OpenList 后台 `设置 → 其他` 复制 **令牌(token)**;
3. Doriya → 设置 → 存储源 → 类型选 `OpenList / AList`:
   - 地址:`http://<openlist主机>:5244`
   - Token:粘贴上面的令牌(不填则要求该路径允许游客访问);
4. 建媒体库 → 扫描。

### 直连(302)的注意点

- 网盘直链一般**绑定出口 IP 和时效**。家里宽带下,服务器和手机走同一个公网出口,302 直连没问题;
  如果手机在外网(流量)、服务器在家,直链可能 403 —— 播放器里点 **强制转封装** 即可
  (转封装模式下由服务器拉流,IP 与取链时一致,总是可用)。
- 若 OpenList 对该存储设置了"代理",`fs/get` 返回的是 OpenList 自己的 `/p` 链接,同样能 302。

### WebDAV 源

WebDAV 源的直链通常需要鉴权头,浏览器拿不到,所以 Doriya 对 WebDAV 一律走服务器代理
(支持 Range 拖动)。想白嫖 302 直连,请用 OpenList 源。

## 刮削

- 来源按优先级:**本地 nfo/海报 → TMDB**(每个媒体库可配置,勾选即启用);
- 本地 nfo 支持 Kodi 格式:`movie.nfo` / `tvshow.nfo` / `*.nfo`、`poster.jpg`、`fanart.jpg` 等
  (支持网盘目录里直接读取);
- TMDB 需要在 设置 → 刮削与播放 里填 API Key(免费);国内网络可在设置里把
  API/图片地址改成镜像或自建反代;
- 详情页可对单个条目 **重新识别**。

## 播放 / API

| 端点 | 说明 |
| --- | --- |
| `GET /api/play/{fileId}?client=ios&force=auto` | 返回播放决策(模式、URL、字幕、音轨) |
| `GET /api/stream/{fileId}` | 302 直连或 Range 代理(带签名) |
| `GET /api/hls/{fileId}/index.m3u8` | 按需生成的 HLS(VOD 播放列表 + 每片段实时 ffmpeg) |
| `GET /api/subs/{fileId}/{subId}.vtt` | 外挂/内嵌字幕转 WebVTT |
| `GET /api/img/{itemId}/{poster\|backdrop\|still}` | 海报/背景图 |

HLS 分片是**按需生成**的(每个片段请求时现场 `ffmpeg -ss`,转封装直拷),不写临时文件、
不预转整部片,seek 到哪生成到哪。播放 URL 都带 HMAC 签名,播放器无需带 Cookie。

## 开发

```
webui\        Vue3 + Vite 前端(pnpm dev 热更新,代理 /api → 5666)
cmd\doriya\   服务端入口
cmd\mockopenlist\  OpenList v3 API 的本地 mock(测试 302 链路用)
scripts\e2e-test.ps1   端到端冒烟测试(先启动 doriya + mockopenlist + testdata)
testdata\     ffmpeg 生成的测试片源(h264/hvc1/hev1/mkv-eac3/剧集+字幕)
```

调试前端:`pnpm dev`(端口 5166)+ 后端 `.\doriya.exe`,浏览器开 http://127.0.0.1:5166。

## 已知限制 / Roadmap

- [ ] iOS Safari 原生 HLS 播放时,外挂 VTT 字幕的 `<track>` 支持不稳定(建议用内嵌文字字幕,或等 HLS 字幕轨方案);
- [ ] PGS/图形字幕只在转码模式下有效(烧录),暂不支持外挂;
- [ ] 多音轨在 302 直连模式下由浏览器选择(通常第一条),HLS 模式下可切换;
- [ ] 剧集季/集刮削依赖 Show 已匹配 TMDB;手动指定 TMDB ID 在详情页"重新识别"里;
- [ ] 多用户/权限、播放队列、WebDAV 直链透传、天翼云盘原生驱动(免 OpenList)在计划中。
