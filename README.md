# Codex CLI for Android

把 **OpenAI Codex CLI** 装进安卓手机，让它能连**任何 OpenAI 兼容接口**（`chat/completions` 或 `responses`）。

Run OpenAI **Codex CLI** on Android as a Magisk / KernelSU module, with a built-in local gateway and a web console.

刷入后**不需要改任何文件**：浏览器打开控制台，填上接口地址 / API Key / 模型就能用。模块里不预置任何地址、密钥或个人信息。

## 特性

- **一键刷入**：Magisk / KernelSU 模块，重启即用，卸载会自动清理运行目录
- **自带本机网关**：在手机上把 Codex 的 Responses 请求翻译成 `chat/completions`，所以只提供 `chat/completions` 的中转站、自建服务也能直接用
  （Codex 0.132 起不再支持 `wire_api = "chat"`，只会发 Responses 请求；没有这层网关，`chat/completions` 上游就用不了）
- **网页控制台**：浏览器直接聊天。左侧会话列表（可折叠/展开）+ 主聊天区 + 左下角「设置」，交互与 Codex 桌面版类似
- **处理过程实时可见**：聊天气泡下方有一条活动栏，逐条滚动显示当前执行的命令与进度；历史消息里的每一步命令都收在「处理过程」折叠块里，默认不糊屏
- **明暗主题**：默认跟随系统，也可以手动切「亮色 / 暗色 / 跟随系统」
- **不把良性提示当报错**：命令退出码非 0、上游的良性提示用浅色提醒，只有真正的失败才是红色
- **技能管理**：左侧「技能」面板列出手机上已安装的 skill，可直接删除（系统 skill 受保护）
- **定时任务**：左侧「定时任务」面板增删改查，支持 `every 30m` / `daily 09:00` / `cron 0 9 * * *`，到点自动把内容派给 Codex 并写进新会话
- **接口与模型随时换**：网页里改，或手机上用 `codexctl set-*` 改，立即生效；点顶部模型标签即可快速切换模型
- **可选 `devctl`**：让手机当 SSH 客户端去管理其他机器
- **文件上传**：控制台底部「上传文件」可把文件传到 Codex 工作目录，后续对话能直接读取/处理（同名自动加 `-1`/`-2`，不覆盖）
- **Python / C 开发环境**：`su -c codexctl toolchain` 一键安装 Python 3.12 与 C/C++ 编译器（zig cc），提供 `python3`/`pip3`/`cc`/`gcc`/`clang`/`c++`/`g++`
- **文件管理器**：左侧「文件」面板浏览工作目录，支持编辑/保存/下载/新建文件与目录/重命名/删除/全文搜索，像桌面版一样直接在网页里看文件、改文件
- **沙箱与审批可配置**：设置里可切 `danger-full-access` / `workspace-write` / `read-only` 沙箱与审批策略（注意：Android 内核沙箱受限，`read-only`/`workspace-write` 可能无法真正生效，默认保持 `danger-full-access`）
- **图片附件**：发送框旁可附加图片，会保存到工作目录并在气泡里显示缩略图；受上游模型能力限制，非视觉模型只能拿到文件路径

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

## 网页控制台：技能 / 定时任务

### 技能（Skills）

左侧「技能」按钮列出 `<CODEX_HOME>/skills` 下所有 skill（含 `.system`）。每条显示名称、说明、文件数和体积：

- 自装的 skill 可以点「删除」移除（不可恢复，会 `rm -rf` 整个目录）；
- `.system` 下的是内置系统 skill，面板里标「受保护，不可删除」。

### 定时任务

左侧「定时任务」按钮管理计划任务：

| 字段 | 说明 |
|---|---|
| 名称 | 随便起，留空则取任务内容前 24 个字 |
| 任务内容 | 到点发给 Codex 的 prompt |
| 时间计划 | `every 30m` / `every 2h` / `daily 09:00` / `cron 0 9 * * *`（支持 `*`、`*/N`、`a-b`、逗号列表） |

面板里每条任务可以「立即运行 / 暂停·启用 / 删除」，并显示下次执行时间和上次结果。到点执行时结果会写进一个标题为 `[定时] 名称` 的新会话，在左侧会话列表里看。

计划任务存在 `/data/adb/codex/web/tasks.json`（权限 600）；网关每 15 秒巡检一次。

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

**聊天里出现黄色的「提醒：Model metadata for ... not found」？**

这是上游返回的良性提示（自定义模型没有完整元数据），**不是失败**，不影响对话。控制台把它标成浅色提醒而不是红色失败。真正的失败才会显示红色。想细看可以 `su -c codexctl log`。

**同一轮对话里命令卡片为什么不重复了？**

Codex 对每条命令会先后发 `item.started` 和 `item.completed` 两个事件。网关只把 `completed` 落盘、并按 item id 合并，所以历史回放时一条命令就是一张卡片。

**模型列表拉不出来？**

多半是接口地址填错（多了 `/chat/completions` 之类）或 Key 没权限。执行 `su -c codexctl models` 看上游到底返回了什么。

**DNS 解析失败 / 换网络后连不上？**

模块自带 `/etc/resolv.conf`（安卓原生没有这个文件）。换网络后执行 `su -c codexctl dns` 刷新。

**TLS 证书错误？**

模块启动时会把系统 CA 打包成 `/data/adb/codex/ca-bundle.crt` 并通过 `SSL_CERT_FILE` 指给 codex。用自签证书的话，把 CA 追加到该文件末尾再 `codexctl restart`。

**怎么卸载 / 会留东西吗？**

Magisk App 里移除模块并重启即可。`/data/adb/codex`（配置、密钥、会话、codex 主程序）会在卸载时自动删除 —— 里面有你自己填的 API Key，需要的话先备份。

**手机上没有 python / node / bash？**

安卓本来就没有。本模块只用 `sh`、`tar`、`curl`，不需要额外装任何东西。；若想在手机上跑 Python / C 代码，装 `codexctl toolchain`（见上）。


## 开发环境（Python / C）

需要在本机跑 Python / C 代码时，先装开发环境（Python 3.12 + zig 编译器）：

```sh
su -c codexctl toolchain
```

装完后可用：

```sh
python3 -V                    # Python 3.12.x
pip3 install requests
cc hello.c -o hello && ./hello
g++ main.cpp -o main
```

- `codexctl toolchain python` 只装 Python；`codexctl toolchain cc` 只装 C/C++；`codexctl status` 会显示版本。
- C/C++ 由 zig 提供：`cc`/`gcc`/`clang`/`c++`/`g++` 都是 zig 的包装器，目标为 `aarch64-linux-musl`。
- 首次安装需联网下载（Python 约 70 MB、zig 约 43 MB），失败后重跑一次即可；已下载文件会复用、支持断点续传。
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
  scripts/fetch-toolchain.sh 下载安装 Python + C 编译环境
  scripts/resolv.sh         生成 /etc/resolv.conf
src/codexweb/               本机 LLM 网关 + 网页控制台（Go，零第三方依赖）
  main.go                   配置读写 / 路由 / codex 配置同步
  handlers.go               各 API、SSE 事件流（含 item 去重）
  runner.go                 调 `codex exec --json`，良性提示分类
  skills.go                 /api/skills 列出与删除 skill
  tasks.go                  /api/tasks 定时任务 CRUD + 调度循环
  translate.go              Responses <-> chat/completions 协议转换
  upload.go                /api/upload 控制台文件上传
  files.go                 /api/files 文件浏览/编辑/搜索
  ui/index.html             单文件网页控制台（主题 / 活动流 / 技能 / 定时任务）
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
- 网关还负责写 `~/.codex/config.toml`、生成 `auth.json`、把上游的良性提示标成浅色提醒（而不是红色失败），让 codex 在安卓上安安静静跑起来。
- 会话与任务落盘在 `/data/adb/codex/web/`：`threads/*.json`（会话）、`tasks.json`（定时任务）。
- 手机没有 `/etc/resolv.conf`，模块会在挂载层补一个，并把系统 CA 汇总给静态编译的 Go / musl 二进制。

## 许可

MIT，见 [LICENSE](LICENSE)。

## 免责声明

本项目只是把官方 Codex CLI 打包进安卓，并附带一个本机的协议转换网关。Codex CLI 版权归 OpenAI 所有。接口费用、账号风控、以及你所在地区的法律法规请自行确认 —— 请只连接你自己有权使用的接口。
