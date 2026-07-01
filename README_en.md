# tdl

<img align="right" src="docs/assets/img/logo.png" height="280" alt="">

> 📥 Telegram Downloader, but more than a downloader

English | <a href="README.md">简体中文</a>

> [!NOTE]
> This is a fork of [iyear/tdl](https://github.com/iyear/tdl) that adds a local **Web UI** for multi-account management and browser uploads. See the [Web UI](#web-ui) section.

<p>
<img src="https://img.shields.io/github/go-mod/go-version/iyear/tdl?style=flat-square" alt="">
<img src="https://img.shields.io/github/license/iyear/tdl?style=flat-square" alt="">
<img src="https://img.shields.io/github/actions/workflow/status/iyear/tdl/master.yml?branch=master&amp;style=flat-square" alt="">
<img src="https://img.shields.io/github/v/release/iyear/tdl?color=red&amp;style=flat-square" alt="">
<img src="https://img.shields.io/github/downloads/iyear/tdl/total?style=flat-square" alt="">
</p>

#### Features:
- Single file start-up
- Low resource usage
- Take up all your bandwidth
- Faster than official clients
- Download files from (protected) chats
- Forward messages with automatic fallback and message routing
- Upload files to Telegram
- Export messages/members/subscribers to JSON
- **Local Web UI** for multi-account login and browser uploads *(this fork)*

## Preview

It reaches my proxy's speed limit, and the **speed depends on whether you are a premium**

![](docs/assets/img/preview.gif)

## Web UI

A lightweight, self-hosted web UI added by this fork — multi-account management and uploads from the browser, built on top of the existing tdl engine.

Start it with:

```bash
tdl server          # alias: tdl web
```

By default it listens on `127.0.0.1:8080` — open <http://127.0.0.1:8080> in your browser.

| Flag | Default | Description |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Address to bind |
| `-P`, `--port` | `8080` | Port to listen on |

What it offers:
- **Multi-account login** via verification code; switch the active account right in the browser
- **Telegram-style chat console** — each conversation's uploads render as message bubbles
- **Send files** by drag-and-drop or file picker (photos / videos / files, and albums), or by a server-local path
- **Backup / restore** — download a `.tdl` backup or restore one from the settings dialog (interchangeable with the CLI `backup`/`recover`)
- **Task overview** with live upload progress

> [!WARNING]
> The API has **no authentication** — bind it to localhost only and never expose the port to the internet. It shares the same bolt storage as the CLI, so don't run other tdl processes while the server is running.

## Documentation

Please refer to the [documentation](https://docs.iyear.me/tdl/).

## Sponsors

![](https://raw.githubusercontent.com/iyear/sponsor/master/sponsors.svg)

## Contributors
<a href="https://github.com/iyear/tdl/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=iyear/tdl&max=750&columns=20" alt="contributors"/>
</a>

## LICENSE

AGPL-3.0 License
