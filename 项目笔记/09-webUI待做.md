# 09 · 网页版待做清单（web UI ← CLI 功能缺口）

对照 CLI 全部命令盘点出的 **web UI 还没做、但值得做** 的功能，含优先级、可复用的底层、依赖关系。目的：三个月后想给 web 加功能时，不用重新通读代码就知道「做什么、复用谁、坑在哪、先做哪个」。

> 现状与已做功能见 [06-网页版.md](06-网页版.md)。这里只列**缺口和规划**，做完某项后把它从本表移到 06 的「已做」并补实现要点。

## 一、CLI 能力 × web 现状对照

web 现有接口（`app/web/server.go` 路由）只覆盖：账号/登录（仅验证码）、会话列表、上传、任务、限速代理、备份恢复。

| CLI 命令 | 能力 | web 现状 |
|---|---|---|
| `up` 上传 | 文件/目录/相册/caption | ✅ 已做（比 CLI 更强：预览、进度环、取消） |
| `chat ls` | 列会话 | ✅ 已做（左栏列表 + TG 自定义分组） |
| `login -T code` | 验证码登录 | ✅ 已做 |
| `backup`/`recover` | 备份/恢复 | ✅ 已做 |
| 全局 `--rate` | 上传限速 | ✅ 已做；**下载限速 ❌ 占位禁用** |
| **`dl` 下载** | 链接/JSON 下载、断点续传、命名模板、serve 流式 | ❌ **完全没做** |
| **`chat export`** | 拉取会话消息 → JSON | ❌ 没做（web 目前"只发不收"，看不到已有消息） |
| **`chat users`** | 导出频道/群成员 | ❌ 没做 |
| **`forward`** | 转发/克隆/表达式路由 | ❌ 没做 |
| `login -T qr` | 扫码登录 | ❌ 没做 |
| `login -T desktop` | tdata 导入 | ❌ 没做 |
| `migrate` | 切换存储驱动 | ❌ 没做（web 场景意义低） |

## 二、待做功能（按优先级）

### 🔴 P0 · 下载 —— tdl 的立身之本，web 版最大空缺

无下载的 "Telegram Downloader" 网页版名不符实。建议做**两种语义**（面向不同场景，都要有）：

- **A. 流式回浏览器**：server 从 TG 拉流 → 直接回给浏览器另存/在线预览播放，**不落 server 磁盘**。
  - **复用点**：[app/dl/serve.go](../app/dl/serve.go) 已实现 `partio.NewStreamer` + `tg_io` + `http_io.NewHandler`，**已支持 Range 断点**（浏览器 `<video>` 拖进度条、`<img>`、`<a download>` 直接对接）。这套逻辑几乎是为 web 量身定做的，抄进 `app/web/` 加一个 `/download/{peer}/{message}` 路由即可。
  - 适合"下到我人在的这台机器"。
- **B. server 落盘**：复用 [app/dl/dl.go](../app/dl/dl.go) 的 downloader，带断点续传/命名模板/并发分片/`--group` 整组。
  - 适合 server 本身就是目标机（如 NAS 挂 tdl）。

- **前置依赖**：得先能定位消息。**最小成本先做「粘贴 t.me 链接下载」**（复用 `pkg/tmessage` 解析链接），快速见效、不依赖别的；完整形态依赖 P1 的消息浏览。
- **顺带补下载限速**：设置弹窗里已有占位（禁用状态），和上传限速对称，成本极低。上传限速的共享 limiter 做法见 [08-带宽限速.md](08-带宽限速.md)，下载限速挂在 `core/downloader/progress.go` 的 `writeAt.WriteAt` 单点。

### 🟠 P1 · 会话消息浏览（拉 history）—— 让 web 从"发件箱"变"真客户端"

目前右栏只显示自己发出的任务气泡，**看不到会话里已有的消息**。做完后能列出会话历史/媒体，勾选即可下载或转发。它是 **P0 完整下载和 P2 转发的共同入口**。

- **复用点**：消息迭代可参考 `chat export`（[app/chat/export.go](../app/chat/export.go)）的拉取逻辑。
- **难点**：分页、媒体缩略图、topic/相册分组、增量刷新。难度中上。

### 🟡 P2 · 转发 forward

web 里选消息（链接或 P1 勾选）→ 选目标会话 → `direct`/`clone` 模式、`silent`、`dry-run`。

- **复用点**：[app/forward/forward.go](../app/forward/forward.go)（受保护内容自动 clone fallback 已内置）。
- 目标选择和左栏会话列表天然契合。依赖 P1 体验最佳，但"粘贴链接转发到某会话"可先独立做。

### 🟢 P3 · 导出类（轻量，快速见效）

都是"点按钮 → 后端跑现成逻辑 → 回下载/表格"，UI 成本低。

- **chat export**：把会话消息导出成 JSON 下载，可套 filter 表达式、time/id/last 范围。复用 [app/chat/export.go](../app/chat/export.go)。
- **chat users**：导出频道/群成员成 JSON 或网页表格。复用 [app/chat/users.go](../app/chat/users.go)。

### 🔵 P4 · 登录方式补全

当前仅验证码。底层能力都已存在，只差 web 桥接（参考 `app/web/login.go` 把阻塞式 auth flow 桥成异步 HTTP 的做法）。

- **QR 扫码登录**：web 展示二维码手机扫，比验证码快。底层 [app/login/qr.go](../app/login/qr.go) 已有。
- **tdata 导入**：指定/上传 Telegram Desktop 的 tdata 目录导入。底层 [app/login/desktop.go](../app/login/desktop.go) 已有。

### ⚪ P5 · 杂项增强（可选）

- **任务持久化**：当前任务表是内存态，重启 server 清空（见 06）。配合下载任务更有意义。
- **`migrate` 存储驱动迁移**：web 场景价值低，可不做。

## 三、建议动手顺序

1. **P0-A 流式下载（粘贴链接）** 最该先做：价值最高、复用 `serve.go` 现成 streamer、不依赖别的、几天见效。顺手补下载限速。
2. **P1 消息浏览** 打通"能看到消息"，为下载/转发提供完整入口。
3. **P2 转发 / P3 导出** 顺势铺开。
4. **P4 登录补全 / P5 杂项** 视需要。

## 四、关键洞察（通读代码才知道的）

- **`serve.go` 是 web 下载的金矿**：它已经解决了"TG 媒体 → HTTP 流 + Range 断点"，web 下载别从零写 downloader，先抄它。
- **"定位消息"是下载/转发的共同前置**：web 现在只有"发"没有"收/看"，所以任何"对已有消息操作"的功能，都卡在"先得拿到 peer + message id"。最小解是链接输入，完整解是 P1 消息浏览。
- **两种下载语义别混为一谈**：流式回浏览器（server 不落盘）vs server 落盘（带断点/模板），面向的部署场景不同，UI 上要让用户能选。
