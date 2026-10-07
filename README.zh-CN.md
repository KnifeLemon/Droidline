<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/droidline-logo-dark.png">
    <img src="docs/assets/droidline-logo.png" alt="Droidline" width="300">
  </picture>
</p>

# Droidline

[English](README.md) · [한국어](README.ko.md) · **简体中文**

用任何编程语言控制安卓手机，每个操作一行代码。不需要 ADB，不需要 USB 数据线，不需要 root。

```python
from droidline import connect

d = connect()                                   # 已与这台电脑配对的手机
d.launch("com.tencent.mm")
d.dump("screen.json")                           # 在这里查看 id 和文字
d.touchById("com.tencent.mm:id/login")
d.input("id", "com.tencent.mm:id/account", "knife")
d.sendkey("enter")
if d.exists("text", "关闭广告"):
    d.touch("text", "关闭广告")
d.tap(540, 1200)                                # 只有 tap 使用坐标
d.batch([("airplane", True), ("sleep", 3000), ("airplane", False)])  # 更换 IP
```

网站与文档：<https://droidline.dev/zh/>

## 工作原理

```
你的代码 ── SDK / CLI / MCP / curl ──► droidline serve（电脑）◄── Droidline 应用（手机）
               localhost:8780                                手机主动连接
```

* 手机上只安装一个应用，包含无障碍服务、一个输入法，以及按需使用的按应用 VPN。手机主动连接电脑，所以手机上不需要开放端口，也不需要 `adb forward`。
* 电脑上运行 `droidline serve`。你的代码向 `localhost:8780` 每次发送一行命令。
* 等待、重试和坐标回退都由手机和服务器完成。`touch` 最多等待 10 秒让目标出现；如果节点拒绝点击，就改为点击它 bounds 的中心。
* SDK 函数、通信命令、CLI 子命令和 MCP 工具名称一一对应，全部由 [`spec/commands.json`](spec/commands.json) 生成。

手机可以在同一 Wi-Fi 下，也可以通过端口转发、隧道，或经你自己部署的中继使用移动数据连接。握手之后的每一行都经过端到端加密，中继和隧道只能看到密文。详细规范见 [`spec/PROTOCOL.md`](spec/PROTOCOL.md)。

## 快速开始

1. **电脑端服务器。** 从 [Releases](https://github.com/KnifeLemon/Droidline/releases) 下载对应系统的 `droidline`，或用 Go 1.26 以上版本构建：
   ```bash
   go install github.com/KnifeLemon/Droidline/server/cmd/droidline@latest
   ```
   运行后保持开启：
   ```bash
   droidline serve
   ```
2. **手机应用。** 从 [Releases](https://github.com/KnifeLemon/Droidline/releases) 安装 `droidline-agent.apk`。应用不上架 Google Play。打开应用后按清单完成设置：无障碍、Droidline 输入法、通知权限、电池优化例外。Android 13 及以上版本中，旁加载的应用需要先在“应用信息”里选择“允许受限制的设置”，才能开启无障碍，应用内会指引位置。
3. **配对。** 在电脑上运行：
   ```bash
   droidline pair
   ```
   用应用扫描二维码。没有摄像头时，在应用中点“在此 Wi-Fi 下配对”，再把手机上显示的 6 位代码输入电脑：`droidline pair 482913`。
4. **第一条命令。**
   ```bash
   droidline touch text "设置"
   ```

手边没有手机？`droidline-fakephone` 会模拟一台带演示应用的手机，可以先试用 SDK、CLI 和 MCP：

```bash
go install github.com/KnifeLemon/Droidline/server/cmd/droidline-fakephone@latest
droidline-fakephone            # 显示代码后运行 droidline pair <代码>
droidline launch dev.droidline.demo
```

## 在各语言中使用

| 接口 | 安装 | 示例 |
|---|---|---|
| Python | `pip install droidline` | `connect().touch("text", "确定")` |
| Node.js / TypeScript | `npm install droidline` | `await (await connect()).touch("text", "确定")` |
| CLI | 随服务器提供 | `droidline touch text 确定` |
| HTTP | 无需安装 | `curl -X POST localhost:8780/devices/_/touch -H 'content-type: application/json' -d '{"by":"text","value":"确定"}'` |
| MCP | 随服务器提供 | `droidline mcp` |
| 其他任何语言 | 一个 TCP 套接字 | 发送 `{"id":1,"cmd":"touch","by":"text","value":"确定"}`，再读取一行 |

MCP 客户端配置（Claude Desktop、Claude Code、Cursor 等）：

```json
{ "mcpServers": { "droidline": { "command": "droidline", "args": ["mcp"] } } }
```

## 按元素操作，而不是按像素

`dump()` 以树的形式返回当前界面。每个节点都有 `text`、`id`、`desc`、`class`、`bounds`，这些名称就是第一个参数：

| `by` | 匹配 | 简写 |
|---|---|---|
| `text` | text 完全匹配 | `touchByText` |
| `textContains` | text 部分匹配 | |
| `id` | resource-id；只写 `"login"` 也匹配 `"<包名>:id/login"` | `touchById` |
| `desc` | content-desc 完全匹配 | `touchByDesc` |
| `descContains` | content-desc 部分匹配 | |
| `class` | 类名，通常与 `nth` 一起使用 | |

`exists`、`which`、`checked`、`get_text`、`in_app` 等判断命令会立即返回，目标不存在时也不会报错，可以直接放进 `if`。

## 做不到的事

Droidline 只使用普通应用能获得的权限，因此有些事做不到，最好提前知道：

* 无法打开其他应用未导出的界面。`launch` 会改为打开该应用的启动界面。
* 强行停止、清除数据，以及移动数据、Wi-Fi、飞行模式的开关，是通过打开系统设置并点击按钮完成的。每次需要几秒，按钮文字因厂商而异。参考机型为三星和 Pixel。
* 游戏、部分 WebView 和自定义界面没有无障碍节点。这类界面请使用 `tap` 坐标和 `color(x, y)`。
* Android 15 及以上版本会对应用隐藏通知中的验证码。`wait_notification` 能告诉你验证码到了，号码需要打开应用后用 `get_text` 读取。
* 按应用代理需要 Android 10 及以上，且一台手机同时只能开启一个 VPN。忽略系统代理设置的应用会断网，而不是绕过代理。
* 用 `sendkey` 发送键码或文字，以及读取剪贴板，需要 Droidline 输入法为当前输入法。

## 仓库结构

| 路径 | 内容 |
|---|---|
| [`spec/`](spec) | `commands.json`（全部命令）、`PROTOCOL.md`、加密测试向量 |
| [`server/`](server) | Go：`droidline`（服务器、CLI、MCP）、`droidline-relay`、`droidline-fakephone` |
| [`agent/`](agent) | 安卓应用（Kotlin） |
| [`sdk/python`](sdk/python)、[`sdk/node`](sdk/node) | 官方 SDK，由命令定义生成的代码加一层薄客户端 |
| [`relay/worker`](relay/worker) | Cloudflare Workers 中继 |
| [`scripts/gen.mjs`](scripts/gen.mjs) | 从 `commands.json` 生成 SDK 方法 |

## 当前状态

版本 0.1，尚未正式发布。服务器、CLI、MCP 适配器、中继和两个 SDK 已在模拟手机上通过测试。安卓应用已通过构建和单元测试，下一步是各厂商真机测试，设置宏尤其需要真机反馈。验证内容和方法见 [`docs/STATUS.md`](docs/STATUS.md)。

安全问题请见 [SECURITY.md](SECURITY.md)。请只在你拥有或获准自动化的手机和账号上使用 Droidline，用途见[使用条款](https://droidline.dev/zh/terms/)。

## 许可证

MIT，见 [LICENSE](LICENSE)。
