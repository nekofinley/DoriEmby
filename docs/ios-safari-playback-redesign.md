# DoriyaMedia iOS Safari 播放体验重构方案

> 目标读者：DoriyaMedia 维护者。本文只做设计，不含代码改动。
> 背景：iOS Safari 下 HLS 转码起播需 5–6 分钟，播放 1–2 分钟后卡死；HLS 转封装长时间无响应。
> 目标场景：**以 Safari 网页播放为主**（不依赖 nPlayer 等原生 App）。

---

## 0. 结论摘要

「把解码做进浏览器」在 iOS Safari 上不可行，且**不是本问题的解**：

1. iOS/iPadOS Safari **没有 MSE**（Media Source Extensions），`hls.js` 那条 JS 解封装 + MSE 的路在 iPhone/iPad 上不存在。
2. iOS 16.4+ 有 WebCodecs `VideoDecoder`，但只产出 `VideoFrame`，需要自建 WebGL/WebGPU 渲染、音画同步、字幕、进度、画中画——等于自己写播放器；且 1080p HEVC 10bit 高帧率内容走 JS 渲染管线不现实。
3. 即便浏览器能解码，**仍要把原始 MKV 完整下载**。当前的瓶颈是「读得太慢/读得太碎」，不是「解不动」。
4. nPlayer 流畅的原因不是「本地解码」，而是**原生播放器的顺序 Range 拉流 + 系统硬解 + 大缓冲**：读取是顺序的、一次性的。当前服务端是碎片化重复读。

真正的根因是服务端管线两处结构性问题（第 2 节有量化），修复方向是**让服务器只顺序读一遍源**，而不是让客户端解码。

---

## 1. 现状链路（已核对代码）

### 1.1 起播路径

```
Player.vue:loadDecision()
  → GET /api/play/{id}?client=ios
      play_handlers.go:58  EnsureProbe()        ← 同步阻塞
          hls.go:102       probe 缓存未命中 → 远程 ffprobe
          probe.go:151     -analyzeduration 20s -probesize 50M
      media.Decide()                            ← 决策
      返回 mode/url/alternates
  → attachSource() → startCand(0)
  → iOS: v.src = /api/hls/{id}/index.m3u8?...
      play_handlers.go:387  播单（再次 EnsureProbe，理论上已缓存）
      play_handlers.go:413+ 每个分片 → hls.go:153 Segment()
          = 一个新 ffmpeg 进程 + 远端 seek + 重新探测
```

### 1.2 决策结果（针对你的库）

库内实测分布：`files=2271`（`mkv 1808 / 1991.6 GB`，`mp4 463 / 152.4 GB`），**仅 20 个文件有 `probe` 缓存**。
典型内容：`[VCB-Studio] … [Ma10p_1080p][x265_flac].mkv`。

| 检查点 | 代码位置 | 结果 |
| --- | --- | --- |
| 容器 MKV | `decision.go:127-131` | ❌ Safari 不支持 MKV |
| 10bit H.264 | `decision.go:153-155` | ❌ 无硬解（HEVC 场景不适用） |
| 转封装可行性 | `decision.go:219-221` | ❌ `profile="Main 10"` 含 "10" → 禁止视频直拷 |
| 音轨 FLAC | `decision.go:170-177` | ❌ Safari 不收 FLAC-in-HLS → 转 AAC |

→ 这类片源被判为 **`transcode`（全量 x264 重编码）**，而不是转封装。

---

## 2. 根因与量化

### 2.1 根因 A：探测同步且昂贵

- `EnsureProbe` 在 `/api/play` 里**同步**执行，前端只能转圈；
- `probesize=50MB` + `analyzeduration=20s` → 实测约 **59.6 MB** 必须从网盘读完后才回决策；
- 只有 20/2271 命中缓存 → **每个新片第一次播放都要付一次这个代价**。

慢链路下这一项单独就能造成「分钟级无响应」。这也解释了「不知道该等还是失败」——因为 API 根本没返回。

### 2.2 根因 B：分片级读取放大（25×）

每个 4 秒分片独立 spawn 一次 ffmpeg，每次都要：

| 读取项 | 量级 |
| --- | --- |
| ffmpeg 启动探测（`-probesize 50M`） | ~50 MB |
| 远端 seek 到 `-ss` 位置 | 1 次 Range 往返 + 可能回退 |
| 真正产出分片 | 1080p × 4s ≈ 2 MB |
| **合计每片** | **~52 MB** |

- 读取放大 ≈ **25×**；
- 连续播放所需有效带宽 ≈ **10 MB/s ≈ 80 Mbps**；
- Safari 原生 HLS 会**并行预取多个分片**，N 个 ffmpeg 同时抢远端 → 实际更糟；
- ≥1080p 时单个 ffmpeg 就远超实时速度 → **永远追不上**，表现为「播 1–2 分钟后卡」。

### 2.3 根因 C：iOS 特定行为被忽略

| 行为 | 现状 | 影响 |
| --- | --- | --- |
| AVPlayer 对 4xx/5xx 响应**不重试**，直接致命错误 | `play_handlers.go:456-458` 分片失败只 log，返 200 空体 | 一次网络抖动 = 播放终止 |
| 播单一次性下发全部 1800 片 `#EXTINF` | `hls.go:206-237` | AVPlayer 按 4s 粒度预取，等不到就 stall |
| `#EXT-X-PLAYLIST-TYPE:VOD` | `hls.go:222` | 只能用于**完整**播单，增量播单场景语义错误 |
| 无 `#EXT-X-MAP` / fMP4 init | mpegts 输出 | 可用，但无法做 byterange 方案 |
| 客户端无「正在准备第 N 片」状态 | `Player.vue:486-490` | 用户无法区分「加载中」与「挂了」 |
| `isIOS` 会误判 macOS Safari | `Player.vue:38-41`、`api.js:64` | 桌面 Safari 也被当成 iOS（能力其实不同） |

### 2.4 根因 D：转封装判据过严

`hlsVideoCopyOK`（`decision.go:214-226`）的两条：

- **10bit H.264 一律禁止直拷** —— 这是对的（MSE 场景），但 **iOS 原生 AVPlayer 能硬解 Hi10P**；也就是说在 iOS 上我们把本可直拷的内容降级成了全量转码；
- **HEVC 直拷要求 `ClientSafari`** —— 方向对，但没区分 macOS Safari（有 MSE）与 iOS Safari（无 MSE）。

这两条合起来，把你库里大量内容从「转封装」推到了「全量转码」，把 #2.2 的放大效应从「贵」变成了「不可能」。

---

## 3. 设计目标与非目标

**目标（按优先级）**

1. iOS Safari 首次起播：**目标 ≤ 10 秒**（源可达带宽 2 MB/s 前提）。
2. 连续播放**不卡**：远端读取量 = **1×**（顺序读一遍），与码率匹配即可。
3. 用户始终知道在发生什么：探测中 / 准备第 N 片 / 生成速率 / 失败原因。
4. 二次播放同一文件：**秒开**（复用缓存产物）。
5. seek：从目标时间点**重建会话**，恢复时间 ≤ 15 秒，而不是重读 50 MB。

**非目标**

- 不做浏览器端解码 / WebAssembly 软解（第 0 节）。
- 不做整片预转码入库（2 TB 库，磁盘不允许；仅做**有上限的会话级缓存**）。
- 不实现 HLS 多码率自适应（单码率 + 用户手动切画质更符合现状）。

---

## 4. 方案总览

```
                    ┌───────────────────────────────────────────────┐
播放请求 ──────────► │ 1. 快速决策层 (Decider v2)                    │
                    │    · 只读已缓存的 probe，缺失则返回 preparing  │
                    │    · 后台异步补探测，完成即回填               │
                    └───────────────┬───────────────────────────────┘
                                    │ 决策 = 4 条路径
        ┌───────────────────────────┼───────────────────────────┬──────────────────┐
        ▼                           ▼                           ▼                  ▼
  A. 302 直连              B. 渐进代理 (新增)          C. 分片直取 (新增)      D. 会话转码/转封装
  MP4/H.264/AAC           MP4 可播但源不可 302        MP4 + byterange        MKV / 不可播编码
  不占服务器带宽           Range 透传，AVPlayer 自己拉  零 ffmpeg，1× 读取      长驻 ffmpeg，顺序读一遍
```

核心变化：**把「按需逐片转码」换成「一次会话、顺序读源、产物落缓存」**；能在客户端原生播的一律不碰 ffmpeg。

---

## 5. 详细设计

### 5.1 快速决策层（Decider v2）

**现状**：`/api/play` 阻塞等 ffprobe。

**改为两段式**：

```
GET /api/play/{id}          → 立即返回（<200ms）
{
  "state": "ready" | "probing",
  "mode": "hls_session" | "direct" | "proxy" | "byterange" | ...,
  "url": "...",                  // state=ready 时可用
  "probe_progress": {...},       // state=probing 时给前端展示
  "alternates": [...],
  "capabilities": {...}          // 见 5.6
}
```

- 命中 `files.probe` → `state=ready`，直接回决策；
- 未命中 → 入队后台探测任务，`state=probing` 立即返回；前端轮询 `GET /api/play/{id}/status`（或 SSE）直到 ready；
- 探测参数从 `50MB/20s` 降到 **`probesize=8M` + `analyzeduration=4s`**，并在库扫描阶段就**批量后台预探**（限流，例如 2 并发），让绝大多数文件在用户点开前已就绪；
- 探测结果除了现有 JSON，额外落库一份**定位信息**（见 5.4）：moov 位置、moof 存在与否、`sidx`、关键帧索引（若解析成功）。

**状态机**

```
idle → probing → ready
              ↘ probe_failed (可重试, 记录原因)
ready → session_starting → streaming → ended / failed
```

### 5.2 路径 D：会话化转码 / 转封装（解决 MKV + 10bit HEVC + FLAC）

**替换** `hls.go:153 Segment()` 的「一片一进程」为**每会话一个长驻 ffmpeg**。

```
ffmpeg -hide_banner -nostdin
       -analyzeduration 4M -probesize 8M        # 不再吃 50MB
       -i <target>                              # 远端，只连一次
       -map 0:<videoIndex> [-map 0:a:<ord>]
       -c:v copy | -c:v libx264 ...             # 按决策
       -c:a aac -b:a 192k -ac 2
       -f hls
       -hls_time 6
       -hls_playlist_type event                 # 生成期是 event，完成后追加 ENDLIST
       -hls_segment_type fmp4                   # 或 mpegts，见 5.2.2
       -hls_flags independent_segments+temp_file
       -hls_segment_filename <cache>/seg_%05d.m4s
       -master_pl_name master.m3u8
       <cache>/index.m3u8
```

要点：

1. **源只读一遍**（顺序）。远端读取量从 25× 降到 ~1×；ffmpeg 自身有 `-reconnect`，长连接比每片重连稳得多。
2. **产物落盘缓存**，服务器直接由 `http.ServeFile`/`ServeContent` 提供（带 `Cache-Control: public, max-age=31536000, immutable`），不再经过 ffmpeg。
3. **播放列表增量**：ffmpeg 自己写 `index.m3u8`，生成期是 `event` 类型；`-hls_playlist_type event` 期间 AVPlayer 可以播放并等待新分片（这是「边转边播」的正确语义），全部生成完后再补 `#EXT-X-ENDLIST` 变成完整 VOD。**不要**再用一次性算好的 1800 片 VOD 播单。
4. **缓存目录与淘汰**：`data/transcode/<sessionKey>/`，`sessionKey = fileID + 视频/音频选择 + 高度档位`。加 LRU + 容量上限（可配置，默认如 20 GB），播放中的会话加 pin 不淘汰。
5. **二次播放秒开**：命中同一 sessionKey 且已有 `ENDLIST` → 直接吐缓存播单，完全不碰 ffmpeg。这是收益最大的一条（追剧/回看同一片）。
6. **会话生命周期**：
   - 客户端停止请求分片超过 `idleTimeout`（如 60s）→ 杀掉 ffmpeg，保留缓存（保留期独立于进程）；
   - 会话上限（如 3 个并发）→ 超出时淘汰最久未用且未 pin 的会话；
   - 播放页离开（`beforeunload`/`visibilitychange`）→ 上报显式结束，加速回收。

7. **转封装（copy）路径同样会话化**，且放宽判据（见 5.5）：MKV + H.264 8bit 这类只需换容器，长驻 ffmpeg 转封装通常 ≥10× 实时，几乎瞬间就攒够缓冲。

**5.2.1 seek 模型**

seek 到 T 秒 = **新建一个以 `-ss T` 起点的会话**（新的 cache 目录），播单时间轴从 0 开始，前端在客户端侧做时间轴偏移映射（进度条显示 `T + currentTime`）。

- 可选优化：`-copyts -start_at_zero`，或对 MP4 源用 `-ss` 输入 seek（关键帧对齐，快）；
- 向用户提示「正在从 xx:xx 重新准备」，而不是静默 stall；
- 旧会话延迟回收（保留 30s，防止用户抖动来回 seek 反复重转）。

**5.2.2 分片容器选择**

| 选择 | 优点 | 缺点 |
| --- | --- | --- |
| `mpegts`（现状） | iOS 兼容最好，实现简单 | 无法 byterange；对 HEVC 的 tag 处理不像 fMP4 可控 |
| **fMP4 (`-hls_segment_type fmp4`)** | 支持 `#EXT-X-MAP` + byterange；HEVC 可控制为 `hvc1`；体积略小 | 需要 init segment，路径 D 也要保证 init 先就绪 |

建议：**新会话统一用 fMP4**，并在转封装 HEVC 时强制 `-tag:v hvc1`（直接消除 `decision.go:163-165` 那条 hev1 问题）。

### 5.3 路径 C：MP4 分片直取（零 ffmpeg，收益/风险比最高）

针对库内 **463 个 MP4**（以及可被 remux 判定的 MP4 系容器）：

1. 只读文件头尾（`moov` 可能在尾部，读 last 16 MB；有 `sidx` 更好），解析出：
   - 每轨的 sample table / chunk offsets（→ 可以直接换算字节区间）；
   - 关键帧位置（`stss`）；
   - `codec_tag`（`hev1`/`hvc1`）、`codec_string`、分辨率、时长。
2. 生成 **`#EXT-X-BYTERANGE` 版 HLS 播单**，分片直接指向 `/api/range/{id}?start=&end=`，服务器做 **Range 透传**：
   - 该端点把请求转成对源的 `Range: bytes=a-b` 并流回，**只搬客户端真正要的字节**；
   - 需要对 `hev1→hvc1` 做**字节级改写**：因为改写仅发生在 `moov` 内的 sample description box，属于少量固定偏移的字节替换，可在 Range 响应里按偏移打补丁；
3. 关键帧对齐：分片边界取关键帧，`#EXTINF` 用 sample 时间戳算出的精确时长，避免 AVPlayer 卡在非 IDR 上。

**收益**：整个播放过程 **零 ffmpeg 进程、零 CPU**，服务器只是带补丁的字节中转；远端读取 = 客户端实际所需字节（约 1×）。iOS AVPlayer 原生支持 `EXT-X-BYTERANGE`（VOD 场景稳定）。

**风险**：需要一个可靠的 MP4 `moov` 解析器（Go 侧自研或移植）。这是本方案里**唯一需要新写的复杂组件**，建议独立成 `internal/media/mp4box`，配单测（用现成的 `testdata` 片源）。

**兜底**：解析失败或 box 结构异常 → 退回路径 B（渐进代理）。

### 5.4 路径 B：渐进代理（补齐现有 `proxy` 模式的效率）

现状 `proxyRemote`（`play_handlers.go:328-360`）已支持 Range 转发，但：

- 没有对源的 **206/Content-Range 做校验**；源若不支持 Range（回 200 全量），当前会把整个文件从 0 开始灌给客户端 → 表现就是「永远加载中」。**必须**在探测阶段就确认源的 Range 能力（`Accept-Ranges` / 一次 `Range: bytes=0-0` 探测），不支持则直接判为不可用并切其他路径；
- 缺少 `HEAD` 支持语义（路由已注册 `HEAD /api/stream/{id}`，但 `proxyRemote` 对 HEAD 的处理需确认不回 body）；
- 需要在同一次 play 决策里通过 `capabilities` 告知前端「此源不支持 Range，不要走代理」。

**iOS 上的重要性**：MP4 + H.264/AAC + 源不可 302（WebDAV 场景）时，渐进代理就是最优路径——AVPlayer 自己控制缓冲与 seek，服务器零转码。

### 5.5 决策矩阵重写（`media.Decide` v2）

关键修正：

| 项 | 现状 | 改为 |
| --- | --- | --- |
| 10bit H.264 (`Hi10P`) | 一律禁止直拷（`decision.go:153`） | 区分客户端：**iOS Safari 允许 copy**（AVPlayer 硬解），MSE 客户端仍禁止 |
| client 分类 | `safari` 一档（`decision.go:19-29`） | 拆成 **`ios-safari` / `macos-safari`**（macOS 有 MSE，iOS 没有），UA 判定按平台而非 `Macintosh+touch` 的兜底 |
| HEVC copy | 仅 `ClientSafari` | iOS Safari 且输出 fMP4 + `-tag:v hvc1` |
| FLAC 音轨 | 转 AAC | iOS Safari：HLS 里可尝试 `-c:a copy`（AVPlayer 支持 FLAC-in-fMP4 存疑）→ **探测一次能力后决定**，默认仍转 AAC |
| 决策输出 | `mode/url/alternates` | 增加 `path`（`direct`/`proxy`/`byterange`/`session`）、`capabilities`、`seek_model` |

**能力探测（一次性，客户端自报）**：前端启动时跑一组 `canPlayType` + `MediaSource.isTypeSupported`（存在性检查）+ `VideoDecoder.isConfigSupported`（存在性检查），上报后端缓存到会话，避免后端靠 UA 猜。

### 5.6 客户端（`Player.vue`）改造

1. **状态可视**：把 `waiting`/`stalled`/`progress` 映射成明确文案：
   - 「探测媒体信息…」→「正在准备第 N 片（生成速率 x.x×）」→ 播放中；
   - 生成速率从后端状态接口拿（`segments_ready / duration_ready` 对比 `currentTime`），**这是判断「会卡死还是会追上」的关键指标**，比转圈有价值得多。
2. **错误处理对齐 AVPlayer 语义**：
   - 分片请求失败必须返回**可重试**语义（后端：会话存在但该片尚未生成 → 返回 `404` + `Retry-After: 2`，或直接阻塞到生成完再返回，二者择一但要一致）；
   - 前端监听 `error` 时区分 `MEDIA_ERR_NETWORK` / `MEDIA_ERR_DECODE`，网络类**自动从最近分片重试**而不是整条候选链往下切。
3. **`isIOS` 判定收紧**：用 `navigator.userAgentData?.platform` 或 `platform === 'iPhone'|'iPad'`，避免 macOS Safari 被当 iOS（`Player.vue:38-41`、`api.js:64` 两处都要改）。
4. **候选链顺序**：iOS 上把 `session-hls` 排第一、`proxy` 第二、`direct` 最后（因为 iOS 上 MKV 直连必然失败，先试它是浪费一次起播时间）。
5. **保留原生控件**（`nativeControls`）不变——这是 iOS 上唯一稳的选择。
6. 字幕：iOS 上 HLS 字幕轨走 `#EXT-X-MEDIA:TYPE=SUBTITLES` + WebVTT 分片（`Player.vue:454-457` 的 `<track>` 方案在原生 HLS 下不可靠，README 已知限制里也提到了）。PGS 只能烧录，且**应按需烧录 + 单独缓存 key**（不同字幕轨 = 不同 session）。

### 5.7 观测与可诊断性

新增日志/指标（这是这次问题排查最大的教训——**现在完全无法从外部判断卡在哪**）：

```
probe_ms{file, bytes_read, result}
session_start_ms{file, path}
segment_ready_delay_ms{file, seg, generated_at}
session_realtime_factor{file, x}         # 生成速度 / 播放速度
range_probe_supported{source}
client_capabilities{ua_hash, mse, webcodecs}
```

并在 `/api/system/info` 或播放页暴露一份「本片播放诊断」：容器/编码/决策路径/已用时间/生成速率。

---

## 6. 分阶段实施计划

| 阶段 | 内容 | 预期效果 | 风险 |
| --- | --- | --- | --- |
| **P0** | 降低探测参数（8MB/4s）+ 异步探测 + 状态接口 + 前端状态文案 | 起播从「分钟」到「秒」；不再「不知道在等还是挂了」 | 低 | ✅ **已完成** |
| **P1** | 扫描期后台批量预探 + 探测结果缓存落库 | 绝大多数文件点开即 ready | 低（注意限流，避免扫描把带宽打满） | ✅ **已完成** |
| **P2** | 会话化转封装/转码 + 分片缓存 + 增量 event 播单 + 缓存淘汰 | 消除 25× 放大；连续播放不卡；二次播放秒开 | 中（会话生命周期、磁盘占用） | 🟡 **部分完成**：HEVC 直拷已走会话（fMP4/event 播单，真机 ma10p 可播）；转码仍按需、无缓存复用 —— 见 §12 |
| **P3** | 放宽 copy 判据（iOS Hi10P）+ fMP4 + `-tag:v hvc1` + 客户端能力自报 | 更多片源从「全量转码」降级为「转封装」 | 中（需要真机验证各机型） | 🟡 **部分完成**：fMP4 + `hvc1` 已落地；能力仍是手动开关 `apple_hevc_copy`，未做自报 |
| **P4** | MP4 `moov` 解析 + `EXT-X-BYTERANGE` 直取 + Range 补丁 | MP4 全部零转码、零 CPU | 高（新解析器，需充分测试） | 未开始（`internal/media/mp4box.go` 已提供 box 解析底座，见 §12） |
| **P5** | 渐进代理加固（Range 能力探测、206 校验、HEAD） | WebDAV/不可 302 场景可用 | 低 |
| **P6** | 字幕轨（HLS WebVTT）+ PGS 按需烧录缓存 | 补齐已知限制 | 中 | 🟡 **部分完成**：字幕已能自动载入（服务端排序 + `default` 标记），侧挂/内嵌文本轨按需转 WebVTT；**未做** HLS 字幕轨（`EXT-X-MEDIA`）与 PGS 烧录 |

先做 P0 + P1，能立刻回答「到底卡在探测还是卡在分片」；P2 是根治读取放大的主刀。

---

## 7. 验证方案

1. **本地可复现**：用 `testdata/` 里的 `mkv-eac3 / hev1 / hvc1` 片源 + `cmd/mockopenlist`，在 mock 层注入**人为延迟与限速**（例如 3 MB/s、每请求 +300ms RTT），使问题可稳定复现并度量。
2. **指标门槛**：P0 后 `/api/play` P95 < 300ms（未命中 probe 时也如此）；P2 后 `session_realtime_factor > 2×` 才算通过。
3. **真实链路**：`scripts/browse-live-test.ps1` 扩展为：起播耗时、首片耗时、连续 5 分钟无 stall、seek 恢复耗时、二次播放耗时。iOS 真机手测（Safari + 低电量模式各一遍）。
4. **回归**：确保 `direct`/`proxy` 路径与桌面浏览器（macOS Safari / Chrome + hls.js）不回退。

---

## 8. 附：为什么不选其他方案

| 方案 | 否决原因 |
| --- | --- |
| hls.js + MSE 在 iOS | iOS Safari 无 MSE，能力不存在 |
| WebCodecs 自建播放器 | 需自研渲染/同步/UI；10bit HEVC 1080p60 走 JS 渲染不现实；不解决带宽 |
| ffmpeg.wasm 客户端软解 | 手机 CPU 功耗与性能不可行 |
| 整片全量预转码入库 | 2 TB 库、磁盘与时间成本不可接受 |
| 纯提高 `segment_seconds` | 只把放大倍数从 25× 降到 ~13×，未解决根因 |
| 客户端 JS 里做 MKV→MP4 remux | 仍需 MSE 才能播；且要先下完整个文件 |
| 只买带宽/换 CDN | 25× 放大下不经济；且 WebDAV/鉴权源无法绕过服务器 |

---

## 9. 一句话总结

**不要让浏览器解码，要让服务器少读。** 当前 5–6 分钟起播是「同步 ffprobe 拉 60MB」+「每 4 秒分片重启 ffmpeg 重读 50MB」的叠加；把探测异步化/廉价化，把转码改成「一会话一进程、顺序读一遍、产物落缓存」，iOS Safari 的体验就能回到可用甚至好用——而这条路不需要任何浏览器端解码能力。

---

## 10. 实施状态

### 10.1 已完成：P0 + P1

| 项 | 实现 |
| --- | --- |
| 探测窗口可配置 | `playback.probe_size_mb`（默认 8）、`probe_analyze_sec`（默认 4）；同时是**上限**而非常量读取 |
| 深度探测兜底 | 小窗口读不出流时自动用 50MB/20s 重试一次，识别正确性不受影响 |
| 异步探测 | `/api/play` 立即返回 `state=probing`，后台任务执行；`/api/play/{id}/status` 返回 `idle/probing/ready/failed` + 已用时 + 失败原因 |
| 单飞与冷却 | 同一文件并发只探一次；失败冷却 30s，避免反复打源；HLS/字幕同步路径会**join** 正在跑的异步任务 |
| 无视频流处理 | 探测到「有流但无视频轨」时记为失败并给出原因，避免前端无限轮询 |
| 扫描后预探测 | `OnScanned` 钩子 → `FilesWithoutProbe` → 后台串行预热（限数量、限并发、每文件间隔 400ms） |
| 前端状态可见 | 探测中显示「正在探测媒体信息… Ns」；缓冲中显示「正在缓冲… 已等待 Ns」；等待超 25s 直接给出「强制转封装 / 强制转码 / 重试」按钮 |
| 设置项 | 设置 → 刮削与播放 → 播放 里可调上述全部参数 |

### 10.2 实测结果（`scripts/e2e-test.ps1`，已改为探测感知）

| 断言 | 结果 |
| --- | --- |
| 首次 `/api/play` 立即返回（无缓存探测） | **2 ms**，`state=probing`，且**不带** url |
| 轮询状态 → 就绪 → 拿到决策 | `probing` → `ready`，决策 `remux` |
| 预探测让首次播放即热缓存 | 未调用过 `/api/play` 的文件 `state=ready`，首次决策 **2 ms / `state=ready`** |
| 既有行为无回归 | 302 直连（302 + CDN Range 206）、Range 代理（206 + Content-Range）、HEAD、HLS 转封装（分片 + TS 同步字节 + seek）、字幕 VTT 全部通过 |

### 10.3 一处需要澄清的实测发现（不要高估 P0-1 的收益）

用自写的**按字节计数的 HTTP 服务器**实测 ffprobe 真实读取量：

| 场景 | 50MB/20s | 8MB/4s |
| --- | --- | --- |
| 77MB MP4（moov 在尾，流易识别） | 读 6.66 MB / 241 ms | 读 6.13 MB / 83 ms |
| 32MB MKV（30MB 封面附件在前） | 读 32.29 MB | 读 32.29 MB |

结论：

1. `probesize` 是**分析缓冲上限**，ffprobe 只读到够用为止——所以「50MB → 8MB」不等于「读取量降 6 倍」；
2. 封面附件很大的 MKV，无论窗口多大都会读到附件之后（本例是整文件）；
3. **但这三档窗口（8/16/50MB）在所有测试文件上都识别出了相同的流**，没有出现漏流；
4. 因此真正消除「分钟级无响应」的是 **P0 的异步化**（响应 2ms + 进度可见），
   以及 **P1 的预探测**（点开即热缓存，等待根本不发生）；探测窗口的作用是把最坏情况的上界压下来。

（这也说明文档第 2.1 节「一次拉 60MB」应理解为**最坏情况**，而不是每个文件的实际读取量。）

### 10.4 尚未实现（下一步是 P2）

P2（会话化转封装/转码 + 分片缓存 + 增量 event 播单）**尚未实现**——即 2.2 节的
「每 4 秒分片重启 ffmpeg、25× 读取放大」问题依然存在。这是「播 1–2 分钟后卡顿」的根因，
也是下一步收益最大的一刀。P0/P1 解决的是「起播等太久且不知道在等什么」。

---

## 11. 第二轮：进度显示、「有声音没画面」与 fMP4 评估

### 11.1 已实现：分片生成进度与实时倍率

`/api/play/{id}/status` 新增 `stream` 段：

```json
"stream": { "segments_requested": 3, "segments_ready": 2, "segments_failed": 0,
            "generated_seconds": 8, "realtime_factor": 33.19,
            "last_segment_ms": 120, "last_segment_bytes": 863296, "idle_seconds": 0.4 }
```

播放页据此显示**确定进度条**（已就绪 Ns / 12s）与**编码速度 x× 实时**。低于 0.9× 且等待
超过 20 秒时，直接提示去降低「转码最高高度」。

一个实现上的坑（已修正）：`realtime_factor` 的分母**必须是「实际干活的时间」**，不能用
「自本轮开始以来的墙钟时间」——播放器缓冲充足时根本没有生成动作，用墙钟会把速度算得极低，
甚至在极快返回时除以 0。现在按每个分片的实际耗时累计。

### 11.2 已修复：「HLS 转封装有声音没画面」的真正原因

**根因不是容器，是强制转封装绕过了编码能力校验。** `Decide()` 原先对 `force=remux` 直接放行：

```go
if force == "remux" || force == "transcode" {   // 旧代码：无条件放行
    d.Mode, d.Kind, d.Reason = force, "hls", "手动指定 " + force
```

于是用户在 **AV1（`svtav1`）** 文件上点「强制转封装」时，服务器把 AV1 原样拷进 MPEG-TS。
AV1 **任何客户端都解不了**——PC 的 hls.js 不行，iOS 的 AVPlayer 也不行——结果就是
**有声音、有字幕、没有画面**，而且 PC 与 iOS 表现一致。这与用户描述完全吻合。

修复：`force=remux` 现在也要通过 `hlsVideoCopyOK(video, client)`，不通过就退化为转码，
并在原因里写明，例如：

```
手动指定转封装,但 HEVC 无法被本机解码(只有声音没有画面),已改为转码
```

自动决策路径本来就是对的（AV1 → 转码），已用测试钉住防止回归。

### 11.3 fMP4 评估结论：**可行，但必须放在 P2 里做，不能按需逐分片生成**

Apple 的 HEVC-in-HLS 走 fMP4（ffmpeg 官方邮件列表有一封 2025-07 的补丁，标题即
「fix HEVC fmp4 HLS init segment for Apple playback」，说明 init 段写错会让**所有 Apple 平台**
无法播放，见 <https://ffmpeg.org/pipermail/ffmpeg-devel/2025-July/346920.html>）。

实测（本机 ffmpeg，MKV HEVC + EAC3）：

| 项 | 结果 |
| --- | --- |
| `-tag:v hvc1` | ✅ init 段读出 `hevc,hvc1`（Apple 需要的标签） |
| 从 `ftyp+moov` 切出 init（砍掉第一个 `moof` 之前） | ✅ 3685 字节，ffprobe 可识别两条流 |
| 分片时长约束 | ❌ `-avoid_negative_ts make_zero -t 4` 产出 **12s**（`-t` 与被打乱的时间戳不一致）；✅ 改用 `-copyts -to <start+len>` 后每个位置都精确 4s 且可解码 |
| `tfdt`（HLS fMP4 要求为**累计**媒体时间） | ❌ **`-copyts`、`+frag_discont`、`-output_ts_offset` 三种写法产物完全相同，`tfdt` 一律被 movenc 归零**（每片都从 0 开始） |

最后一条是关键阻塞：按需逐分片生成时，每个分片都是独立的一次 ffmpeg 进程，movenc 会把
首样本时间当作 0，于是所有分片的 `tfdt` 都从 0 开始，而 HLS fMP4 需要的是连续递增的时间轴。

#### 11.3.1 已验证的 P2 配方（本机 ffmpeg 8.0.1，实测通过）

改用 **ffmpeg 自己的 HLS muxer** 后，上面两个问题一次解决——它把 init、`tfdt`、播单
**一次性写对**：

```bash
ffmpeg -i <source> -map 0:v:0 -map 0:a:0 \
       -c:v copy -tag:v hvc1 -c:a aac -b:a 192k -ac 2 \
       -f hls -hls_segment_type fmp4 -hls_time 4 \
       -hls_playlist_type vod -hls_list_size 0 \
       -hls_fmp4_init_filename <绝对路径>/init.mp4 \
       -hls_segment_filename <绝对路径>/seg%05d.m4s \
       <绝对路径>/index.m3u8
```

实测（MKV HEVC + EAC3 源）：

| 项 | 结果 |
| --- | --- |
| 播单 | `#EXT-X-VERSION:7` + `#EXT-X-MAP:URI="init.mp4"` ✅ |
| 分片结构 | `styp + sidx + moof + mdat`（标准 CMAF 形态）✅ |
| **`tfdt` 累计** | 第 0 片 `[0,0]`、第 1 片 `[165984,455688]`；`init+第1片` 解出的首帧 pts = **10.457s**，正好落在播单位置 ✅（按需逐分片永远做不到这点） |
| 解码 | `init + 任意分片` 均可解出帧 ✅ |
| **init 的 fourcc** | 默认写出 **`hev1`（Apple 不接受）**；加 `-tag:v hvc1` 后为 **`hvc1`** ✅ |
| `-tag:v hvc1` 对分片字节的影响 | 无（标签在 init 里），所以**只加在 init 生成的那次调用上也够**，但每次都加最省事 |

两个必须记住的坑：

1. **`-hls_fmp4_init_filename` 是相对进程 CWD 解析的**，不是相对播单路径——只给播单一个绝对
   路径时，`init.mp4` 会被写到**服务器的当前工作目录**（我实测时它落到了仓库根目录）。必须给
   绝对路径或显式设置工作目录。
2. 默认 `hev1` 就是 ffmpeg 官方邮件列表那个「Apple 全平台不可播」的 bug；**必须显式
   `-tag:v hvc1`**。

**结论（已更新）**：fMP4 与 P2 确实是同一件事，但配方已经**验证可行、无未知项**——
把它落地的唯一前提是 P2 的会话化打包（一次长驻进程、产物落盘、播单增量）。在此之前
Apple 的 HEVC 走转码（见 11.5），不会出现「有声音没画面」。

以下为旧结论（保留以便对照，其"阻塞"已被 11.3.1 的配方解决）：

1. ~~**用 ffmpeg 自己的 HLS muxer**~~ → **已采用，见 11.3.1**；
2. 生成后**自行改写 `tfdt`**：需要解析 `mdhd` 取各轨 timescale、按 traf 顺序回填，并且无法
   从输出里还原「首个关键帧的真实起点」（`-ss` 会落到前一个关键帧），会引入每个分片不等量的偏差。
   → 不再需要，方案 1 已覆盖。

**结论**：fMP4 与 P2 是同一件事，应合并实施，配方已在 11.3.1 验证。在 P2 之前，
Apple 的 HEVC 直拷**默认关闭**（`apple_hevc_copy=false`），一律走转码——所以不会再出现
「有声音没画面」。真机若确认 HEVC-in-TS 可播，可以打开该开关省掉全部转码开销。

### 11.4 仍未实施

- **P2（会话化打包）**：一次长驻 ffmpeg + `-f hls -hls_segment_type fmp4`（配方见 11.3.1），
  产物落盘 + 增量 event 播单 + seek 重建会话。它同时解决两件事：Apple 的 HEVC 零开销直拷，
  以及现在「每 4 秒重启一次进程、重新 seek、重新读探测窗口」的读取放大；
- **权限**：工作区里「早于 DSH 接管就存在」的文件（`config.json`、`data/*.db`）对**从工作区内
  启动的非提权进程**不可写（实测：同一个 exe 放到 `%TEMP%` 就能写；shell 自己也能写，只有
  「工作区内的进程」不行）。定向修复需管理员执行：
  `icacls config.json /reset`、`icacls data\doriyamedia.db* /reset`。在此之前用管理员启动即可。

### 11.5 第二轮的行为变更清单（用户可见）

| 变化 | 说明 |
| --- | --- |
| 起播进度可见 | 播放页显示分片生成进度条与「编码速度 x× 实时」；<0.9× 提示降低转码高度 |
| iOS + HEVC | **默认转码**（有画面）；设置里可开 `apple_hevc_copy` 改为直拷 |
| 强制转封装 | 只在该客户端**确实能解码该视频编码**时才生效，否则退化为转码并写明原因 |
| 分片失败 | 返回 502 + `Retry-After`，不再假报 200；云盘链接被拒会重取新链接重试一次 |
| 扫描后预探测 | 默认开启（`preprobe_max_files=100`、并发 1），可在设置里关闭 |

## 12. P2 收尾：会话起点、时间轴与缓存

### 12.1 已完成的部分（真机已验证）

`hls_session_packager` 打开时，**整个 HLS 播放**（转封装与转码都是）走一次长驻 ffmpeg：
`-f hls -hls_segment_type fmp4`、直拷 HEVC 时加 `-tag:v hvc1`，产物落盘、event 播单、
会话空闲即关并删目录。这样每个分片不再各自起一个进程、不再各自重新 seek 并重读探测窗口——
这正是 P2 要消掉的读取放大。真机上 `[Ma10p_1080p][x265_flac]` 全部可播，且 CPU 几乎为零。

副产物：开关打开后，本机 e2e 的决策表里那些 HEVC 片源在 iOS 上从「转码」变成了「转封装」
（因为直拷现在会产出 Apple 能解的 fMP4），这正是 P3 「放宽 copy 判据」想要的方向。

**尚未完成**：seek 到会话窗口外时的重建；分片缓存复用与容量淘汰。

### 12.2 一个必须修的既有缺陷：会话起点

会话从 **0** 开始顺序打包。但播放器的时间轴是绝对的，而「继续观看」会在
`loadedmetadata` 里 `currentTime = 已保存进度`（例如第 40 分钟）——于是播放器要请求第 600 个分片，
而打包器还在第 0 分钟。**恢复播放会长时间等待**，这是当前实现里的真实缺陷。

要让会话从任意位置开始，必须让「分片序号」与「媒体时间轴」都能表达绝对位置。本机实测
（ffmpeg 8.0.1，40s / 2s GOP 的合成源，`-c:v copy`）：

| 手段 | 结果 |
| --- | --- |
| `-start_number K` | ✅ 播单写出 `#EXT-X-MEDIA-SEQUENCE:K`，文件名 `seg0000K.m4s` —— **序号可控** |
| `-ss P`（输入 seek） | ✅ 从 P 起产出，但同时**把输出时间轴归零** |
| `-copyts -output_ts_offset P` | ❌ 与不加完全一样，`tfdt` 依旧从 0 开始（与 11.3 的结论一致） |

结论：**序号能对齐、时间轴不能**。而 fMP4 里播放器是按 `tfdt` 判定媒体时间的，所以「序号对齐
但 tfdt=0」会让播放器把第 K 片当成第 0 片——恢复/seek 时会错位甚至卡死。

### 12.3 解法：按会话起点平移 `tfdt`

既然 ffmpeg 不肯写绝对时间，就在**服务分片时自己补上**：把该分片内每个 `traf` 的
`baseMediaDecodeTime` 加上 `起点秒数 × 该轨 timescale`。轨的 timescale 从 init 段的 `mdhd` 读，
`traf` 属于哪条轨看 `tfhd` 里的 track_ID。

- 实现：`internal/media/mp4box.go`（`ParseInitTracks` / `ShiftTfdt`），只解析需要的几个 box，
  **解析失败一律回退成原样输出**，绝不让补丁影响可播放性。
- 验证（`mp4box_test.go`，真实 ffmpeg 产物）：偏移 +8s 后，`ffprobe` 读出首帧视频 pts
  = **8.0s**（±0.15），且 init+分片仍能解出画面；原文件不被就地修改；垃圾/截断输入返回错误而非 panic。
- 这个 box 解析器同时是 **P4** 需要的 `moov` 解析底座。

仍存的不精确：`-ss` 会落到**前一个关键帧**，所以第一片的内容可能比声明位置早最多一个 GOP。
要完全精确，需要把会话起点对齐到关键帧（或先从源上查关键帧位置）——列为下一步。

### 12.4 P2 剩余工作（顺序）

1. ✅ **会话起点已完成**：`/api/play` 把已保存进度带进 HLS URL，打包器用 `-ss` + `-start_number K`
   从那里开始、并保持绝对分片序号；服务分片时按 12.3 平移 `tfdt`，把媒体放回绝对时间轴。
   验证：`TestSessionStartingMidFile`（原始分片 `tfdt=0`、服务出去的分片首帧 pts=8.0s、播单
   `MEDIA-SEQUENCE:2` + `seg00002.m4s`）；e2e 新增 7d（保存到一半 → `start=` 出现在 URL →
   恢复后的播单 `MEDIA-SEQUENCE:1` → 该分片首帧 pts=4.08s）。
   恢复只在「越过有意义的最小位置（30s，短片按 5% 缩放）且不在最后 5%」时触发。
2. ⏳ 会话键已含起点（`TestSessionKeyIncludesStart`）；**seek 到窗口外时重建会话**尚需接线：
   目前若播放器请求早于会话起点的分片，只会等待超时，而不是另起一个从那里开始的会话；
3. ⏳ 分片缓存 + 容量上限 + LRU 淘汰 + 启动清理残留目录（「二次播放秒开」）；
4. ✅ **转码也走会话**：路由条件从「HEVC 直拷」放宽为「开关打开 + 决策是 remux/transcode」，
   两种模式共用同一套会话机制（`VideoMode` 取自决策）。验证：e2e 7e（强制转码 → 会话播单带
   `#EXT-X-MAP`、分片 `video/mp4`、ffprobe 读出 `h264`、能解出画面）。至此「每分片重启进程」
   在开关打开时已彻底消除。

### 12.6 仍未做（P2 收尾）

- **seek 到会话窗口外**：现在请求早于会话起点的分片只会等到超时并返回 502，应该另起一个从
  那个位置开始的会话（`SessionKey` 已含起点，接线即可）。播放器侧行为（能否反向 seek、seek bar
  范围）我还没在浏览器里量过，量完再定实现方式；
- **缓存与淘汰**：会话结束即删目录（不占盘但也没有复用），要做「保留已完成产物 + 容量上限 +
  LRU 淘汰 + 启动清理残留」，才能实现「二次播放秒开」；
- **开关默认值**：目前 `hls_session_packager` 默认关。打开后每个会话会把该片的分片写到
  `data/cache/hls`（直拷约等于源文件大小），所以先保持 opt-in，等缓存上限做完再讨论是否默认开。

### 12.5 取舍说明（P4/P5 为什么排在后面）

- **P4**（MP4 byterange）：本库 463 个 MP4 多为 H.264/AAC，iOS 本就原生可播；真正需要它的场景是
  「源不可 302 + 客户端只吃 HLS」，而那条路现在由**渐进代理**覆盖，已经是零转码。收益最小、风险
  最高（要新写并充分测试 moov 解析），所以放在 P2/P6 之后。
- **P5**（代理加固）：现在的代理路径已被 e2e 覆盖（Range 206、HEAD、`accept-ranges`），先补
  Range 能力探测与 206 校验即可，成本低，可在 P2 之后顺手做。



