> [!IMPORTANT]
> 中文文档可能落后于英文文档，如果有问题请先查看英文文档。
> 请使用英文发起新的 Issue, 以便于追踪和搜索

# tdl

<img align="right" src="docs/assets/img/logo.png" height="280" alt="">

> 📥 Telegram Downloader, but more than a downloader

<a href="README_en.md">English</a> | 简体中文

> [!NOTE]
> 这是 [iyear/tdl](https://github.com/iyear/tdl) 的 fork，新增了本地**网页版（Web UI）**，用于多账号管理与网页端上传。详见 [网页版](#网页版) 一节。

<p>
<img src="https://img.shields.io/github/go-mod/go-version/iyear/tdl?style=flat-square" alt="">
<img src="https://img.shields.io/github/license/iyear/tdl?style=flat-square" alt="">
<img src="https://img.shields.io/github/actions/workflow/status/iyear/tdl/master.yml?branch=master&amp;style=flat-square" alt="">
<img src="https://img.shields.io/github/v/release/iyear/tdl?color=red&amp;style=flat-square" alt="">
<img src="https://img.shields.io/github/downloads/iyear/tdl/total?style=flat-square" alt="">
</p>

#### 特性：

- 单文件启动
- 低资源占用
- 吃满你的带宽
- 比官方客户端更快
- 支持从受保护的会话中下载文件
- 具有自动回退和消息路由的转发功能
- 支持上传文件至 Telegram
- 导出历史消息/成员/订阅者数据至 JSON 文件
- **本地网页版（Web UI）**，支持多账号登录与网页端上传 *（本 fork 新增）*

## 预览

预览中的速度已经达到了代理的限制，同时**速度取决于你是否是付费用户**

![](docs/assets/img/preview.gif)

## 网页版

本 fork 在 tdl 引擎之上新增的一个轻量、自托管的网页版——可在浏览器里做多账号管理和上传。

启动方式：

```bash
tdl server          # 别名：tdl web
```

默认监听 `127.0.0.1:8080`，在浏览器打开 <http://127.0.0.1:8080> 即可。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--host` | `127.0.0.1` | 绑定地址 |
| `-P`, `--port` | `8080` | 监听端口 |

功能：
- **多账号验证码登录**，可在浏览器里随时切换当前账号
- **Telegram 式聊天台**——每个会话的上传记录以消息气泡呈现
- **发送文件**：拖拽或选择文件（照片 / 视频 / 文件，支持相册），也支持「服务器本地路径」模式
- **数据备份 / 恢复**：设置弹窗里一键下载 `.tdl` 备份、上传恢复（与 CLI `backup`/`recover` 通用）
- **任务总览**，实时显示上传进度

> [!WARNING]
> 该 API **无鉴权**——只应监听本地，切勿把端口暴露到公网。它与 CLI 共用同一份 bolt 存储，server 运行期间不要再启动其它 tdl 进程。

## 文档

请参考 [文档](https://docs.iyear.me/tdl/zh/).

## 赞助者

![](https://raw.githubusercontent.com/iyear/sponsor/master/sponsors.svg)

## 贡献者
<a href="https://github.com/iyear/tdl/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=iyear/tdl&max=750&columns=20" alt="contributors"/>
</a>

## 协议

AGPL-3.0 License
