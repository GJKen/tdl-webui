# AI 声明：tdl-master 项目分析

本文档由 AI 根据本地项目文件静态分析生成，分析对象位于：

`D:\Study\Python\tdl-master`

## 项目定位

`tdl` 是一个使用 Go 编写的 Telegram 命令行工具。项目 README 将其描述为 “Telegram Downloader, but more than a downloader”。它不是单纯的下载脚本，而是围绕 Telegram MTProto API 构建的一套 CLI 工具集。

主要能力包括：

- 从 Telegram 聊天中下载文件，包含受保护聊天内容。
- 转发消息，并在直接转发失败或受限时自动降级为克隆/重上传。
- 上传本地文件到 Telegram。
- 导出聊天消息、成员或订阅者到 JSON。
- 支持 Telegram 登录、session 存储、数据迁移。
- 支持扩展命令机制。
- 提供本地自建的网页版（web UI），支持多账号验证码登录与网页端上传。

## 技术栈

项目主体是 Go 模块，根模块名为 `github.com/iyear/tdl`。核心依赖包括：

- `github.com/spf13/cobra`：构建 CLI 命令。
- `github.com/spf13/viper`：读取 flag、环境变量和配置。
- `github.com/gotd/td`：Telegram MTProto 客户端能力。
- `go.etcd.io/bbolt`：默认本地 KV 存储。
- `github.com/expr-lang/expr`：用于转发、上传、过滤、模板等动态表达式。
- `go.uber.org/zap`：结构化日志。
- `golang.org/x/sync/errgroup`：并发任务控制。
- `github.com/gorilla/mux`：网页版（web UI）的 HTTP 路由。

## 目录结构理解

项目代码分层比较清楚：

- `main.go`：程序入口，只负责上下文、信号中断和顶层错误处理。
- `cmd/`：CLI 命令定义层，负责参数、子命令、flag 绑定和入口分发。
- `app/`：应用业务层，组织下载、上传、转发、聊天导出、登录、迁移等完整流程；其中 `app/web/` 是本地自建的网页版（web UI）业务层。
- `core/`：核心能力层，包含 Telegram client、下载器、上传器、转发器、DC 连接池、存储适配、中间件等。
- `pkg/`：通用工具层，包含表达式、模板函数、路径处理、KV、消息解析、扩展管理等。
- `docs/`：Hugo 文档站点内容。
- `test/`：集成测试和测试数据。
- `extension/`：扩展能力相关模块。

整体调用关系可以概括为：`main.go -> cmd -> app -> core/pkg`。

## 启动流程

程序从 `main.go` 启动：创建可响应 `os.Interrupt` 的 context，调用 `cmd.New().ExecuteContext(ctx)` 执行 Cobra 命令树，并对常见错误（如数据库被占用、用户中断）做友好输出。

`cmd/root.go` 是 CLI 的核心装配点：初始化根命令 `tdl`，注册命令组（账号、工具、扩展），添加子命令（`login`、`download`、`forward`、`chat`、`upload`、`backup`、`recover`、`migrate`、`gen`、`extension`、`server` 等），初始化日志与 KV 存储，处理旧版存储到 Bolt 的自动迁移，并配置代理、命名空间、并发数、限速、NTP、重连超时等全局参数。

## 核心功能分析

### 登录

登录命令定义在 `cmd/login.go`，业务在 `app/login`。支持三种登录方式（通过 `-T` 指定）：

- `desktop`（默认）：从 Telegram Desktop 或其兼容分支（如 AyuGram）的 `tdata` 目录导入已有 session。核心在 `app/login/desktop.go`，使用 `gotd/td` 的 `session/tdesktop` 解析 tdata；`-d` 指定目录（为空则自动探测常见安装路径），`-p` 传桌面端本地密码（passcode）。导入后可选择登出桌面端 session，以避免冲突。
- `code`：`app/login/code.go`，在终端输入手机号与 Telegram 下发的验证码（已登录的客户端会收到，而非短信），若开启两步验证则再输入密码。
- `qr`：`app/login/qr.go`，终端渲染二维码，用手机 Telegram 扫码关联桌面设备。

三种方式都会把 session 写入当前 namespace 的 KV 存储，并标记 app 类型为 desktop。登录的是同一个 Telegram 账号，但不同登录方式或不同 namespace 得到的是相互独立的 session。

### 下载

下载命令定义在 `cmd/dl.go`，实际流程在 `app/dl/dl.go`。支持两种输入：Telegram 消息链接，或官方客户端导出的 JSON 文件。

下载流程大致是：解析出 dialog/message → 创建 DC pool 提高跨数据中心下载能力 → 生成下载迭代器 → 检查断点续传状态 → 创建进度条 → 调用 `core/downloader` 并发下载 → 中断则保存进度，完成则清理 resume key。

`core/downloader/downloader.go` 使用 `errgroup` 控制并发任务，单个文件内部还会使用 gotd downloader 的多线程分片下载能力。

### 转发

转发命令定义在 `cmd/forward.go`，业务流程在 `app/forward/forward.go`，底层能力在 `core/forwarder/forwarder.go`。支持从链接或导出 JSON 读取消息、直接转发、克隆转发、受保护消息的自动 fallback、表达式路由目标、编辑消息文本或 caption、dry-run 模式。

关键设计点是 `ModeDirect` 和 `ModeClone`：非受保护内容优先直接调用 Telegram forward API；受保护内容或直接转发失败时，尝试下载并重上传媒体实现“克隆”。对 grouped message 会识别并批量处理，避免重复发送。

### 上传

上传逻辑在 `app/up/up.go` 和 `core/uploader`。流程包括：扫描本地文件 → 按 include/exclude 过滤 → 解析目标 peer → 编译 caption 表达式 → 创建上传迭代器和进度条 → 并发上传到 Telegram。上传侧支持表达式能力，例如根据文件名、扩展名、MIME 类型生成目标或 caption。

目标 peer 的指定方式（见 `cmd/up.go`）：

- `-c/--chat`：聊天 id 或用户名，可配合 `--topic` 指定话题群中的话题；为空表示发送到 “Saved Messages”（自己）。
- `--to`：基于表达式引擎的路由，可按文件属性把不同文件分发到不同目标，与 `--chat`/`--topic` 互斥。
- 另有 `--photo`（以图片发送）、`--file`（强制按普通文档发送，与 `--photo` 互斥）、`--rm`（上传后删除本地文件）、`--album`（把文件发成媒体组/相册，普通号每组 ≤10，按顺序）。
- 相册模式（`--album` 或网页相册）走 `app/up` 的 `runAlbum`：按顺序上传组内文件、caption 只放第一项、每 ≤10 个用 gotd 的 `message...Album()` 发成一条相册消息（>10 自动分多组）。`core/uploader` 把「上传文件 + 构建媒体」抽成可复用的 `BuildMedia`，单发与相册共用。
- 逐文件发送（`core/uploader/uploader.go` 的 `Upload`）对单个文件的发送错误不会中断整批（除用户取消外），但真实错误会先传给进度回调 `OnDone` 再决定是否继续——CLI 端据此把该文件进度条标红并打日志，网页端据此把任务标 `error`。
- `--caption`：上传后消息的说明文字，本质是 expr 表达式（不是纯文本），对每个文件求值得到字符串后再按 HTML 解析。默认值是表达式 `"<code>"+FileName+"</code> - <code>"+MIME+"</code>"`，所以不指定时默认 caption 是「文件名（去扩展名）- MIME 类型」并渲染成等宽样式。可用字段见 `Env`（`app/up/up.go`）：`FilePath`、`FileName`、`FileExt`、`ThumbPath`、`MIME`；运行 `up --caption -` 可打印全部字段。空表达式即得到空 caption。

peer 解析在 `core/util/tutil/tutil.go` 的 `GetInputPeer`：非数字按用户名解析（`manager.Resolve`），数字按 id 依次尝试 channel/user/chat。私有目标通常需先经 `chat ls` 等操作把 access hash 写入 peer 缓存，才能按 id 解析。`-p` 指定的路径由 `filepath.WalkDir` 遍历（`app/up/walk.go`），不做通配符展开。

### 聊天工具

`cmd/chat.go` 提供三个子命令：`chat ls`（列出聊天）、`chat export`（导出聊天消息）、`chat users`（导出频道/群成员）。这些工具服务于下载和转发流程，例如先导出消息 JSON，再批量下载或转发。

### 网页版（web UI）

这是在原 `tdl` 之上本地自建的功能，并非上游原生能力。命令定义在 `cmd/server.go`，业务实现在 `app/web/`，已注册进 `cmd/root.go` 的工具命令组。

`server` 命令（别名 `web`）在本地启动一个轻量 HTTP 服务，把已有的上传、会话列举等业务逻辑包装成网页接口，主要用于多账号管理和网页端上传。默认监听 `127.0.0.1:8080`，可用 `--host` 与 `-P/--port` 调整。**接口无鉴权，只应监听本地，不要对外暴露。**

整体是「薄 HTTP 层 + 复用现有业务」的结构，主要模块：

- `app/web/server.go`：路由用 `gorilla/mux`，REST 接口在 `/api` 下（账号列表、self、改名/删除、登录流程、会话列举、上传、任务）；新增账号会校验 namespace 文件名合法性；前端静态页用 `go:embed` 内嵌（源文件在 `app/web/ui/`）。
- `app/web/manager.go`（`ClientManager`）：每个 namespace（账号）维持一个常驻、已授权的客户端，靠 `RunWithAuth` 在独立 goroutine 保活，HTTP 请求派发到活客户端执行；每账号用互斥锁串行化重操作避免触发 flood；删除账号时通过 `Drop` 取消 run context、释放存储占用后才删底层文件。
- `app/web/login.go`（`LoginManager`）：网页端登录只支持验证码方式，用 channel 把 gotd 阻塞式 auth flow 桥接成异步 HTTP 输入，状态为 `starting / need_code / need_password / done / error`，单次 5 分钟超时，等价于 `tdl login -T code`；成功后把账号身份缓存到 `web:self`。
- `app/web/upload.go`：把网页请求映射成 `up.Options`，复用 `app/up` 的 `up.Run`。两个入口：`POST /upload`（发送服务器本机已有路径）与 `POST /upload-files`（multipart 接收浏览器文件字节，暂存临时目录发完清理）；网页 caption 是 markdown，转成 Telegram HTML 后包成 expr 字面量塞回既有 expr 管线；支持「作为文件」强制按文档发送；另有 `POST /upload-album` 发成一条相册。每个任务在可取消 context 中运行。
- `app/web/chats.go`：调用 `app/chat` 的 `ListDialogs` 拉取会话并缓存 peer access-hash，把列表存入 `web:chats`。默认 `GET /chats` 直接返回本地缓存、不连 Telegram，`?refresh=1` 才实时拉取覆盖。会过滤已注销账号与重复的「自己」会话，并在 `Dialog` 上附带 `Bot`、`SendForbidden`/`SendForbidReason`（从账号权限与频道/群默认权限算出，零值即可发），供前端禁用无权限会话的发送。
- `app/web/progress.go` / `errs.go`：把单个上传的字节进度写进任务表（供进度圆环），`OnDone` 收到非空错误时把任务标 `error`；权限类错误码（`CHAT_SEND_MEDIA_FORBIDDEN`、`CHAT_WRITE_FORBIDDEN`、`USER_IS_BLOCKED` 等）统一翻译成「无权限发送（错误码）」回填前端。
- `app/web/tasks.go`：内存中的上传任务表，记录状态（`queued / running / done / error`）、所属会话、文件名+大小、caption、是否相册、上传百分比，并持有取消函数；`DELETE /api/tasks/{id}` 取消并移除任务。

前端在 `app/web/ui/`（`index.html` + `app.js` + `style.css`），中文界面，分为：

- **顶部账号栏**：列出所有已登录账号（各 namespace），整行点击切换当前账号，行内可查看/刷新、改名、删除；底部「添加账号 / 登录」弹出登录向导。当前账号用 `localStorage` 记忆并驱动上传。
- **会话标签**：Telegram 式控制台。左侧会话列表（搜索框只过滤已缓存会话，Saved Messages 置顶，右键可置顶/复制 ID/刷新）；右侧把发往该会话的上传任务渲染成「只发不收」的聊天气泡，当前账号发的靠右、其他账号靠左，显示文件名/caption/大小/时间/状态，上传中显示进度圆环 + ✕ 取消。发送支持拖文件或选择文件（可勾「作为相册」、可逐个选照片/视频/文件，带发送效果预览），也支持「服务器本地路径」模式。会话记录在本机多账号间通过 `chat_id` 与各账号 `web:self` id 保持一致（A 发给 B 的文件在双方会话里对称出现）。无权限发送做了事前拦截（红字横幅替换输入框）+ 事后兜底（后端翻译权限错误回填气泡）。
- **任务**：全局总览，每秒轮询展示所有账号的上传任务状态。

登录向导（弹窗）：账号名称（可留空，留空用 Telegram 昵称）+ 手机号 → 验证码 →（按需）2FA；核验期间前端文案固定为「校验中…」避免回跳。改名、删除、错误提示统一用内置弹窗组件替代浏览器原生 `prompt/confirm/alert`。

账号「改名」采用显示别名方案：别名存在该 namespace 的 `web:alias`，不改动 namespace 本身，因此不影响 CLI 默认的 `-n default`。「删除账号」先经 `ClientManager.Drop` 断开常驻连接，再由 `pkg/kv` 的 `DeleteNamespace` 删除该 namespace 全部本地数据。账号列表只列出有 session 的 namespace。

当前限制：网页端登录仍只支持验证码方式，QR / tdata 导入、下载、转发还没做；上传任务表是内存态，重启 server 后清空；头像颜色与置顶状态存在浏览器 `localStorage`。

## 存储设计

项目的存储抽象很薄，核心接口位于 `core/storage/storage.go`：

```go
type Storage interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte) error
    Delete(ctx context.Context, key string) error
}
```

上层通过 `pkg/kv` 管理不同命名空间和底层驱动，提供命名空间级操作 `Namespaces` / `Open` / `DeleteNamespace`，bolt、file、legacy 三种驱动均已实现。当前默认存储是 Bolt，旧版 legacy 存储会在需要时自动迁移。

存储主要承载：Telegram session、peer 缓存、app 配置、下载断点续传状态、命名空间隔离数据。

默认 Bolt 数据库位于用户目录下的 `~/.tdl/data`（Windows 为 `C:\Users\<用户名>\.tdl\data`）。通过 `-n/--namespace` 切换命名空间，不同 namespace 的 session 与缓存互相隔离，默认值为 `default`。

## 数据备份与迁移

`cmd/migrate.go` 提供三个账号相关命令，底层实现在 `app/migrate`：

- `backup`：通过 `MigrateTo()` 读取全部 namespace 数据，JSON 序列化后用 zstd 压缩写入 `<日期>.backup.tdl`（`-d` 可指定文件名）。文件包含 session、peer 缓存与配置，**未加密**，应按账号凭据级别妥善保管。
- `recover`：`-f` 指定备份文件，解压反序列化后由 `MigrateFrom()` 导入当前存储。在新设备上 recover 后即为登录态，无需重新验证。
- `migrate`：`--to` 指定目标存储驱动，把当前数据迁移到另一种存储后端（如 legacy ↔ bolt）。

迁移到新设备也可直接复制 `~/.tdl/data` 文件，但需在 tdl 未运行时操作，且绑定 bolt 格式，通用性不如 `backup`/`recover`。

## 网络与代理

全局 `--proxy` 指定代理，格式为 `protocol://username:password@host:port`，支持 socks5、http 等（见 `core/util/netutil`）；也可通过环境变量 `TDL_PROXY` 设置。若使用网关级 / 透明代理（如 OpenClash），tdl 直接走系统出网，无需该参数。

## 项目优点

- 分层清晰，CLI、业务流程、核心能力、工具包边界明确。
- 充分复用 gotd/td，避免手写 MTProto 协议细节。
- 命令行参数设计完整，支持代理、命名空间、并发、限速、NTP、重连等实际使用场景。
- 下载、上传、转发都做了进度显示和并发控制。
- 对 Telegram 受保护内容有专门处理逻辑。
- 具备文档、测试、Dockerfile、发布配置，工程化程度较高。

## 需要注意的点

- Telegram 相关逻辑复杂度较高，尤其是受保护消息、媒体克隆、DC pool、takeout session。
- 运行项目需要有效 Telegram session 和网络环境，很多功能不适合只靠单元测试验证。
- 项目依赖 Go 1.25.8，环境版本需要匹配。
- 代码里大量行为依赖 Telegram API 语义，维护时需要理解 gotd/td 的对象模型。
- 表达式引擎给用户很大灵活性，也要求参数校验和错误提示足够清晰。
- 网页版（web UI）的 API 无鉴权，仅监听 `127.0.0.1`，切勿对外暴露端口；它与 CLI 共用同一份 bolt 存储（独占锁），server 运行期间不要再启动其它 tdl 进程。

## 建议阅读顺序

1. `README.md` 和 `README_zh.md`：先理解工具目标。
2. `main.go`：理解入口。
3. `cmd/root.go`：理解命令树、全局参数、存储初始化。
4. `cmd/login.go`、`app/login`：理解三种登录方式与桌面 session 导入。
5. `cmd/dl.go`、`app/dl/dl.go`、`core/downloader/downloader.go`：理解下载主流程。
6. `cmd/forward.go`、`app/forward/forward.go`、`core/forwarder/forwarder.go`：理解转发和克隆机制。
7. `cmd/up.go`、`app/up/up.go`、`core/uploader`：理解上传与目标解析。
8. `pkg/tmessage`：理解 Telegram 消息链接和导出文件如何被解析。
9. `pkg/texpr`、`pkg/tplfunc`：理解表达式和模板能力。
10. `cmd/migrate.go`、`app/migrate`、`pkg/kv`、`core/storage`：理解本地数据存储、session 管理与备份迁移。
11. `cmd/server.go`、`app/web`：理解本地自建的网页版（多账号管理 + 网页端上传）。

## 总结

`tdl-master` 是一个成熟的 Telegram CLI 工具项目。它的核心价值不是简单下载文件，而是把 Telegram 登录、会话存储、消息解析、下载、上传、转发、导出、表达式路由和扩展机制整合成一个完整工具链。在此基础上，本地还自建了一个网页版（web UI），把多账号验证码登录与网页端上传搬到了浏览器里。

从代码组织看，这个项目适合作为学习 Go CLI 工程化、Telegram MTProto 客户端封装、并发下载/上传、以及命令行工具架构设计的参考项目。
