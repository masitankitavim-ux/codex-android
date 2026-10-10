# Codex CLI for Android

把 **OpenAI Codex CLI** 装进安卓手机，让它能连**任何 OpenAI 兼容接口**（`chat/completions` 或 `responses`）。

Run OpenAI **Codex CLI** on Android as a Magisk / KernelSU module, with a built-in local gateway and a web console.

刷入后**不需要改任何文件**：浏览器打开控制台，填上接口地址 / API Key / 模型就能用。模块里不预置任何地址、密钥或个人信息。

## 特性

- **一键刷入**：Magisk / KernelSU 模块，重启即用，卸载会自动清理运行目录
- **自带本机网关**：在手机上把 Codex 的 Responses 请求翻译成 `chat/completions`，所以只提供 `chat/completions` 的中转站、自建服务也能直接用
  （Codex 0.132 起不再支持 `wire_api = "chat"`，只会发 Responses 请求；没有这层网关，`chat/completions` 上游就用不了）
- **网页控制台**：浏览器直接聊天。左侧会话列表（可折叠/展开）+ 主聊天区 + 左下角「设置」，交互与 Codex 桌面版类似
- **接口与模型随时换**：网页里改，或手机上用 `codexctl set-*` 改，立即生效
- **可选 `devctl`**：让手机当 SSH 客户端去管理其他机器

## 环境要求

| 项 | 要求 |
|---|---|
| CPU 架构 | **arm64 / aarch64**（只做了这个架构） |
| 系统版本 | Android 8.0 及以上 |
| Root 方案 | **Magisk** >= 20.4 或 **KernelSU** |
| 存储空间 | 至少 500 MB 可用（codex 主程序约 157 MB） |
| 网络 | 首次开机需要联网下载 codex 主程序 |

## 安装

1. 到本仓库的 [Releases](../../releases/latest) 页面下载 `codex_android-vX.Y.Z.zip`

   > 不要下 "Source code"，那是源码，Magisk 装不了。

2. 打开 Magisk App -> 底部「模块」->「从本地安装」-> 选中刚下载的 zip
3. 刷入完成后**重启手机**
4. 首次开机后，模块会在后台自动下载 codex 主程序（约 157 MB，需要联网）。用下面的命令看进度：

```sh
su -c codexctl log
```

看到 `[fetch-codex] done` 就装好了。下载慢或失败时执行 `su -c codexctl fetch` 重试。

## 配置：填接口 / API Key / 模型

装完**不用手改配置文件**，在网页里填就行。

### 1. 打开网页控制台

手机终端（需要 root）执行：

```sh
su -c codexctl web
```

输出类似（`<手机IP>` 是你手机在当前 WiFi 下的地址）：

```text
网页控制台（同一 WiFi 下用浏览器打开）：
  http://<手机IP>:8787/?t=<访问口令>
  http://127.0.0.1:8787/?t=<访问口令>   # 手机本机浏览器
```

把带口令的完整地址复制到**同一个局域网**里的手机或电脑浏览器打开。

> 口令是安装时随机生成的，忘了就再执行一次 `su -c codexctl web`。

### 2. 填三项就完事

第一次打开会自动弹出「设置」（也可以随时点左下角「设置」打开）：

| 字段 | 填什么 | 例子 |
|---|---|---|
| 接口地址 | 你的 OpenAI 兼容接口 base URL，一般以 `/v1` 结尾 | `https://api.example.com/v1` |
| API Key | 你的密钥 | `sk-...` |
| 模型 | 模型名。先点「拉取列表」，再从下拉里选 | `deepseek` |
| 上游协议 | 上游支持原生 Responses 就选 `responses`；只支持 chat/completions 就选 `chat` | `chat` |

点「保存」，**立即生效**，回到聊天界面就能对话了。

> 接口地址只填到 `/v1`，**不要**带 `/chat/completions` 或 `/responses` 后缀。

### 3. 也可以用命令行改

```sh
su -c codexctl set-url   https://api.example.com/v1
su -c codexctl set-key   sk-你的密钥
su -c codexctl set-model deepseek
su -c codexctl set-wire  chat
su -c codexctl restart
```

配置存在 `/data/adb/codex/gateway.json`（权限 600），可以手动编辑，改完 `codexctl restart`。

## codexctl 速查

| 命令 | 作用 |
|---|---|
| `su -c codexctl status` | 看当前配置 / 网关状态 / codex 版本 / DNS / CA |
| `su -c codexctl web` | 打印网页控制台地址（含访问口令） |
| `su -c codexctl token` | 只打印访问口令 |
| `su -c codexctl models` | 拉取上游模型列表 |
| `su -c codexctl test` | 用当前配置跑一次完整自检 |
| `su -c codexctl log [-f]` | 看网关日志 |
| `su -c codexctl restart` | 重启网关 |
| `su -c codexctl fetch` | 重新下载 codex 主程序 |
| `su -c codexctl dns` | 重新生成 `/etc/resolv.conf` |
| `su -c codexctl dev <参数...>` | 透传给 `devctl` |

## 在手机上直接用 codex 命令

```sh
su
codex --version
cd /data/adb/codex/workspace
codex exec "uname -m"
```

`codex` 是一个包装脚本，会自动设置 `CODEX_HOME`、`TMPDIR`、`SSL_CERT_FILE` 等环境变量，然后调用真正的 codex。

## 可选：devctl（用手机管理别的机器）

模块里带了 `devctl`，一个跑在手机上的 SSH / SFTP 客户端。设备清单读 `/data/adb/codex/ssh.json`：

```json
{
  "hosts": [
    { "id": "srv1", "host": "192.0.2.10", "port": 22, "user": "root",
      "key": "keys/srv1/id_ed25519", "note": "举例：内网跳板机" }
  ]
}
```

`key` 是相对路径时会相对 `ssh.json` 所在目录解析；没有 `key` 就写 `password`。

```sh
su -c 'devctl ls'
su -c 'devctl ping srv1'
su -c 'devctl ssh srv1 "uptime; df -h /"'
su -c 'devctl run srv1 /data/adb/codex/tmp/deploy.sh'
su -c 'devctl push srv1 /data/adb/codex/tmp/a.zip /root/a.zip'
su -c 'devctl pull srv1 /root/out.tar.gz /data/adb/codex/tmp/out.tar.gz'
```

## 常见问题

**网页打不开？**

- 确认手机和浏览器在同一个局域网；
- 确认网关在跑：`su -c codexctl status`，没有就 `su -c codexctl restart`；
- 手机上直接用本机地址 `http://127.0.0.1:8787/?t=<口令>` 试试，能开说明是局域网隔离问题。

**聊天报 `Model metadata for ... not found`？**

这是上游返回的良性提示，网关会过滤掉，不会影响对话。真正的错误看 `su -c codexctl log`。

**模型列表拉不出来？**

多半是接口地址填错（多了 `/chat/completions` 之类）或 Key 没权限。执行 `su -c codexctl models` 看上游到底返回了什么。

**DNS 解析失败 / 换网络后连不上？**

模块自带 `/etc/resolv.conf`（安卓原生没有这个文件）。换网络后执行 `su -c codexctl dns` 刷新。

**TLS 证书错误？**

模块启动时会把系统 CA 打包成 `/data/adb/codex/ca-bundle.crt` 并通过 `SSL_CERT_FILE` 指给 codex。用自签证书的话，把 CA 追加到该文件末尾再 `codexctl restart`。

**怎么卸载 / 会留东西吗？**

Magisk App 里移除模块并重启即可。`/data/adb/codex`（配置、密钥、会话、codex 主程序）会在卸载时自动删除 —— 里面有你自己填的 API Key，需要的话先备份。

**手机上没有 python / node / bash？**

安卓本来就没有。本模块只用 `sh`、`tar`、`curl`，不需要额外装任何东西。

## 自行构建

```sh
# 需要 Go 1.24 以上
python3 scripts/build.py     # 产出 build/codexweb-arm64、build/devctl-arm64
python3 scripts/package.py   # 产出 dist/codex_android-vX.Y.Z.zip
```

`scripts/build.py` 支持交叉编译到别的平台，例如 `python3 scripts/build.py --goos windows --goarch amd64`。
`scripts/package.py` 会把 `build/` 里的 `codexweb-arm64`、`devctl-arm64` 自动塞进模块的 `system/bin/` 再打包。

> 仓库里**不提交**编译好的二进制，成品只在 Release 里提供。

## 目录结构

```text
module/codex_android/       Magisk 模块源码（打包时整体进 zip 根目录）
  module.prop               模块元信息
  customize.sh              刷入时执行
  service.sh                开机启动网关
  post-fs-data.sh           早期初始化 + 落地 resolv.conf
  uninstall.sh              卸载时清理 /data/adb/codex
  gateway.json              网关配置模板（不含任何密钥）
  system/bin/codexctl       手机上的管控台
  system/bin/codex          codex 包装脚本
  scripts/fetch-codex.sh    下载 codex 主程序
  scripts/resolv.sh         生成 /etc/resolv.conf
src/codexweb/               本机 LLM 网关 + 网页控制台（Go，零第三方依赖）
src/devctl/                 手机上的 SSH 客户端（Go）
scripts/build.py            交叉编译
scripts/package.py          打包成 Magisk 可刷的 zip
scripts/module-installer.sh Magisk 官方安装器（打包时作为 update-binary）
```

## 工作原理

```text
Codex CLI ──Responses──▶ codexweb（手机本机 127.0.0.1:8787）──chat/completions 或 responses──▶ 你的接口
                              │
                        浏览器 ──HTTP──▶ 网页控制台（同一个端口）
```

- Codex 0.132 之后只发 Responses 请求。网关把 Responses 请求翻译成 `chat/completions`（含流式、工具调用、推理内容），再按「上游协议」设置转发。
- 网关还负责写 `~/.codex/config.toml`、生成 `auth.json`、过滤上游的良性提示，让 codex 在安卓上安安静静跑起来。
- 手机没有 `/etc/resolv.conf`，模块会在挂载层补一个，并把系统 CA 汇总给静态编译的 Go / musl 二进制。

## 许可

MIT，见 [LICENSE](LICENSE)。

## 免责声明

本项目只是把官方 Codex CLI 打包进安卓，并附带一个本机的协议转换网关。Codex CLI 版权归 OpenAI 所有。接口费用、账号风控、以及你所在地区的法律法规请自行确认 —— 请只连接你自己有权使用的接口。
