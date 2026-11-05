# 在飞书里使用 Factory Droid

cc-connect 把本机的 Factory Droid（`droid` 命令行）接到飞书机器人上。你在飞书里发消息，Droid 在你的电脑上读代码、跑命令，再把结果回复到飞书。

## 一、准备

1. 安装 Droid 命令行，并登录一次（在终端运行 `droid`，按提示登录）。也可以不登录，改为在配置里设置 `FACTORY_API_KEY`。
2. 在飞书开放平台创建企业自建应用，开启机器人能力，拿到 `app_id` 和 `app_secret`。飞书侧的详细设置见 `docs/feishu.md`。
3. 下载 cc-connect：
   - Windows：解压 `cc-connect-droid-dev-windows-amd64.zip`，得到 `cc-connect.exe`。
   - macOS / Linux：使用对应平台的二进制文件。

## 二、配置

配置文件默认位置：

- Windows：`%USERPROFILE%\.cc-connect\config.toml`
- macOS / Linux：`~/.cc-connect/config.toml`

最小配置（把尖括号里的内容换成你自己的）：

```toml
language = "zh"

[display]
tool_brief_len = 20        # 每个工具调用只显示一行：序号 + 命令前 20 个字符，不显示工具结果
thinking_messages = false  # 不把 Droid 的思考过程发到飞书

[[projects]]
name = "droid"
admin_from = "<你的 open_id>"   # 允许使用 /shell、/dir、/restart 等管理命令；不需要可删掉

[projects.agent]
type = "droid"

[projects.agent.options]
work_dir = "<Droid 工作的目录，例如 D:\\code\\myproject>"
mode = "auto-low"           # 默认自动化等级，见下文 /mode
# project_paths = [
#   { name = "后端", path = "D:\\code\\backend" },
#   { name = "前端", path = "~/code/frontend" },
# ]
# model = "claude-sonnet-5-5"   # 可选：默认模型，也可以在飞书里用 /model 切换
# cmd = "C:\\Users\\me\\AppData\\Roaming\\npm\\droid.cmd"   # droid 不在 PATH 里时，填完整路径

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "<cli_xxx>"
app_secret = "<xxx>"
allow_from = "<你的 open_id>"   # 只允许你自己使用；可先不填，发 /whoami 查到后再填
progress_style = "compact"      # 工具进度合并成一条消息，原地更新
```

注意：Windows 路径在 TOML 双引号里要写成 `\\`，或者改用单引号：`work_dir = 'D:\code\myproject'`。

配置 `project_paths` 后，cc-connect 会自动启用按工作区隔离的 Droid 实例，并在当前聊天首次开始对话时发送飞书项目选择卡片。选择结果会绑定到当前聊天；开启飞书的 `thread_isolation = true` 时，每个话题可以单独选择项目。后续也可以随时发送 `/workspace select <序号>` 重新选择。

`project_paths` 支持两种写法：

```toml
# 只用路径：卡片名称取目录名
project_paths = ["/Users/me/code/backend", "~/code/frontend"]

# 带自定义名称，便于在飞书下拉卡片里识别
project_paths = [
  { name = "后端 API", path = "/Users/me/code/backend" },
  { name = "前端 Web", path = "~/code/frontend" },
]
```

路径可以是绝对路径、`~/...`，或相对于 `work_dir` 的路径。启用 `project_paths` 后不需要另外设置 `mode = "multi-workspace"`；该配置已经会自动开启多工作区模式。

## 三、启动

Windows（在命令提示符或 PowerShell 中）：

```
cc-connect.exe --config %USERPROFILE%\.cc-connect\config.toml
```

macOS / Linux：

```
./cc-connect --config ~/.cc-connect/config.toml
```

看到 `cc-connect is running` 就说明启动成功。之后在飞书里给机器人发消息即可。

## 四、对话时会看到什么

- **普通回复**：Droid 的文字回复，会流式更新。
- **工具进度**：一条消息里逐行列出工具调用，例如 `🔧 工具 #3: Execute `git status``。
- **权限卡片**：Droid 要执行风险较高的操作时，会弹出卡片，点「允许」或「拒绝」。卡片上显示的是完整命令。
- **提问卡片**：Droid 用 AskUser 提问时，会弹出带选项的卡片。点选项即可回答；也可以直接回复文字，作为自定义答案。
- **回复底部的状态行**：显示模型、推理强度、本轮 token 数（in / out / cache）和上下文占用。

## 五、常用命令

在飞书里发送以下命令。命令支持前缀匹配，例如 `/sw 2` 等同于 `/switch 2`。

### 会话

| 命令 | 作用 |
|---|---|
| `/new [名称]` | 开始一个新会话 |
| `/list` | 列出 Droid 的会话 |
| `/switch <序号>` | 切换到列表里的某个会话，继续之前的上下文 |
| `/search <关键词>` | 按名称或 ID 搜索会话 |
| `/name [序号] <名称>` | 给会话起个名字 |
| `/current` | 查看当前会话 |
| `/history [n]` | 查看最近 n 条消息（默认 10 条） |
| `/stop` | 停止 Droid 当前正在执行的任务 |
| `/workspace` | 查看当前项目路径 |
| `/workspace select` | 重新打开项目选择 |
| `/workspace select <序号>` | 切换到 `project_paths` 中的项目 |

### 模型和自动化等级

| 命令 | 作用 |
|---|---|
| `/model` | 查看可选模型（会话开始后才有列表） |
| `/model <名称或序号>` | 切换模型，会写回配置文件，重启后仍然生效 |
| `/reasoning [级别]` | 查看或切换推理强度：off / low / medium / high / xhigh / max（不同模型可选的不同）。注意：切换后会开始一个新会话 |
| `/mode` | 查看自动化等级 |
| `/mode <等级>` | 切换等级（见下表） |

自动化等级：

| 等级 | 含义 |
|---|---|
| `normal` | 编辑文件、执行命令前都先询问 |
| `spec` | 只做方案设计，不修改文件 |
| `auto-low` | 自动执行文件编辑和低风险命令（默认） |
| `auto-medium` | 还会自动执行安装依赖、本地 git 提交、构建等 |
| `auto-high` | 自动执行所有操作，请谨慎使用 |

### 定时任务

| 命令 | 作用 |
|---|---|
| `/timer` | 一次性提醒或定时执行，例如 `/timer add 30m 检查一下构建结果` |
| `/cron` | 周期性任务，例如每天早上让 Droid 汇总某件事 |

### 其他

| 命令 | 作用 |
|---|---|
| `/help` | 查看全部命令 |
| `/status` | 查看运行状态 |
| `/whoami` | 查看自己的 open_id（用于填 `allow_from` / `admin_from`） |
| `/lang [zh\|en\|...]` | 切换界面语言 |
| `/version` | 查看 cc-connect 版本 |
| `/doctor` | 运行诊断 |
| `/shell <命令>`、`/dir [路径]` | 在本机执行命令、切换工作目录。需要配置 `admin_from` |

### 对 Droid 暂不支持的命令

`/compress`、`/delete`、`/usage`、`/allow`、`/provider`、`/memory`，以及对话进行中用 `/ps` 补充消息。发送后会提示不支持，或者没有效果。

## 六、常见问题

- **报错 `Model version ... is deactivated`**：这是 Factory 那边托管模型的服务暂时不可用，不是 cc-connect 的问题。稍后重试，或用 `/model` 换一个模型。
- **`/model` 列表是空的**：先发一条普通消息，让会话启动起来，再发 `/model`。
- **每次对话占用的 token 较多**：Droid 会把工作目录里的 `AGENTS.md`、`CLAUDE.md` 和已安装的技能列表一起发给模型。日常聊天建议把 `work_dir` 指向一个空目录，写代码时再用项目目录。
- **首次对话只收到项目选择卡片**：在卡片下拉框中选择项目，或发送 `/workspace select <序号>`，然后重新发送刚才的任务即可。
- **找不到 droid 命令**：在 `[projects.agent.options]` 里用 `cmd` 填 droid 的完整路径。
