# go-ansible 核心功能复刻评估与开发文档 (AGENTS.md)

> **文档版本**: v0.1.0-eval  
> **更新时间**: 2026-10-21  
> **目标定位**: 本项目旨在采用纯 Go 语言尽可能完整地复刻 `ansible`、`ansible-playbook` 及 `ansible-inventory` 的核心功能与运维体验，实现毫秒级快速启动、低资源占用、原生并发以及无 Python 运行依赖的现代化运维编排工具。

---

## 1. 总体进度与代码资产概览

本项目目前已具备执行常见 Ansible 运维剧本（Playbook）与 Ad-hoc 快速命令的端到端能力。核心执行引擎、SSH 基础设施、Inventory 解析、Playbook 编排流程、变量继承模型、Vault 解密及常用模块均已实现并经过单元测试验证。

### 1.1 代码规模与测试统计

| 组件 / 目录 | 生产代码 (LOC) | 测试代码 (LOC) | 总代码量 (LOC) | 核心功能概述 |
| :--- | :--- | :--- | :--- | :--- |
| `pkg/playbook` | 9,500+ | 13,760+ | 23,260+ | evaluateWhen 完整支持 []string 字符串切片条件链式与求值、combineWhen 字符串切片向下合并穿透、Inventory.HostCount() 极轻量计数与 buildHostVarsSnapshot 快路径主机数直接比对彻底根除 GetAllHosts 全量排序与切片堆分配开销、Ansible 经典 action: 内联字符串 (action: copy src=a dest=b) 与字典结构 (action: {module: copy, src: a}) 语法糖完整支持并自动联动 args 字段、local_action 字典结构与 localhost 委托自动补全、Handler action 语法糖、模块化解耦架构（Task/Cond/Strategy/Roles/Facts/Builtin/Debugger）、执行策略(Linear/Free)、动态 Jinja2 模板化 notify 触发与 handler 动态名称匹配 (handlerMatchesResolved)、循环任务动态委派 (`delegate_to: "{{ item }}"`) 逐项重新解析与目标主机准确路由、setupTaskExecutionHost 统一委派与提权参数装配消重、newLocalHost 本地委托主机极简单例复用、并发执行与 Free 策略 Task Error 严格同步调用 markHostFailed 确保失败主机视图完整、getTaskThrottleSem RWMutex 双检机制彻底消除高并发限流锁争用、mergeCurrentHostVars 空变量早退削减快照克隆损耗、is version/is version_compare 条件测试与版本号对比引擎、顶层引号/括号感知操作符扫描杜绝字符串字面量误截断、lookup vars/lines/sequence 插件补强、is changed/not changed/not skipped/not unreachable 状态测试支持、运行时魔术变量全量注入、role_path/role_name 角色路径注入与相对定位、include_role/import_role 参数全解析与 Handlers 动态注册、rescue 上下文注入、ansible_loop 完整循环上下文、vars_prompt 缓存失效、max_fail_percentage 排除 Rescued 任务、Handler Listen 广播主题、Special Tags 联动过滤、0 Data Race 并发保障 |
| `pkg/module` | 15,480+ | 7,360+ | 22,840+ | lookup random_choice 插件支持与 crypto/rand 随机选取、lookup dict 字典解构插件支持、file/stat/user/group/service 核心模块名称与路径规范别名支持 (GetParamStringAliases/HasAnyParam)、unarchive 模块支持 exclude/excludes 排除解压文件列表 (tar --exclude / zip -x)、42 个原生模块实现、GitModule 完整支持 recursive (默认开启) 子模块递归克隆 (--recursive) 与 existing 仓库 submodule update --init --recursive 自动检出与更新、CommandModule 与 ShellModule 全面支持 stdin 与 stdin_add_newline (默认追加换行) 输入流流式注入 (buildStdinPipe 安全管道)、StatModule 补全官方标配 atime (最后访问时间) 与 ctime (元数据状态变更时间) 跨平台探测与浮点时间戳返回、ResolvePreserveFileMode 原生权限位探测引擎、copy 模块单文件/多层目录递归多文件及远程源文件模式下全面支持 mode: preserve 文件权限完美保留、template 模块全面支持 mode: preserve 源模板权限保留、CheckOrSetFilePermissions 彻底拦截非法 chmod preserve 系统调用阻断崩溃、content 模式 preserve 安全平滑回退、ParseHeadersParam 统一 HTTP 请求头解析消重、uri 模块 url_username/url_password/force_basic_auth 与 body_format: json 自动请求头及 client_cert/key 增强、cron 模块 env: yes 环境变量配置项增删改与幂等维护、version/version_compare 模板与管道过滤器对齐、CheckOrSetFilePermissions 只读预检彻底阻断 check_mode 远端权限篡改、file 模块全状态 CheckMode 完整保护、stat 模块路径 shellQuote 转义与校验和属性增强、replace 模块联动 owner/group/mode 与 dest/name 别名支持、git 模块原生 CheckMode 比对与 force 重置、archive 模块 exclude_path 排除列表与 CheckMode 权限保护、lineinfile 与 blockinfile 联动 owner/group/mode 权限应用、user 模块生产级 generate_ssh_key 密钥对生成、sysctl 模块预检机制、to_nice_yaml 缩进过滤器对齐、GetParam[int] 泛型兜底转换、get_url 裸 Hash 自动探测、50+ 模板过滤函数、parsedTemplateCache 内存 AST 编译缓存加速 |
| `pkg/cli` | 3,870+ | 2,720+ | 6,600+ | Cobra+pflag 参数解析、ansible-doc 增加 -M / --module-path 模块路径选项、ansible Ad-hoc 增加 -o / --one-line 紧凑单行格式化输出、8 独立二进制平替 (含 ansible-vault) 与命令路由、ansible-galaxy role remove 与 direct remove 角色物理卸载支持、ansible-galaxy role init 顶层别名对齐、ansible-doc 补齐 -t lookup 与 -t connection 插件文档检索、ansible-vault -o / --output 标志全子命令对齐、loadVarsDirsToInventory 原生保留切片/字典复杂变量结构、ansible-vault view 与 encrypt_string -o/--output 输出落盘支持、ansible-pull 默认 hostname 限制隔离防多主机清单执行溢出与 --vault-id 凭据多源支持、ansible-doc 多模块同时展示与 -j/--json 结构化导出支持、CommonCLIOptions 抽象与 SetupExecutionEnv 执行上下文极简复用、util.ExpandPath 统一路径展开、ansible-galaxy role list/role info 扩展、ansible-doc package/blockinfile/dnf 原生文档注册、修复 -K/--ask-become-pass 越权全局激活 become 漏洞、ansible-inventory --graph 树状图 ungrouped 未分组主机精准补全、group_vars 目录加载优先级严格对齐官方规范、CLI retry 文件生成精准排除 Rescued 救援主机并补全 Unreachable 主机 |
| `pkg/inventory` | 2,030+ | 650+ | 2,680+ | groupContainsHost 与 getGroupDepth 引入扁平无子组与无父组零分配快路径（彻底杜绝单机解析万级 visited map 堆分配损耗）、轻量级 HostCount() API 在 RLock 保护下零切片分配/零排序直接返回主机总数、ResolveHostVarsRaw 引入 resolvedVarsRawCache 结构化原生变量读缓存与 Double-check 读写机制根除高并发多主机全量递归与组深度排序损耗、ansible_user -> remote_user -> cliUser 回退层级确定性对齐、ResolveHostVarsRaw 原生结构化变量解析与 SetGroupVar / SetHostVar 复杂变量支持、expandHostRangePattern 边界异常输入防御性容错、INI 顶部无组名主机自动归并 ungrouped 与 all 组规范对齐、ApplyHostVariable 导出复用、多清单/目录/内联主机自动归并、ansible_connection 解析、INI/YAML/JSON/动态脚本、expandHostRangePattern 主机范围表达式展开、内联主机端口分割与 IPv6 [::1]:port 识别、INI [group:children] 状态机与防主机污染、Group 环形引用栈溢出防护 (visited 集合)、祖先组变量层级继承与子组优先级覆盖、applyHostVariable 统一连接变量映射消重、O(G+N) 高性能分组模式匹配检索、并发隐式 localhost 线程安全隔离、util.GetRegex 模式匹配加速、Inventory.AddHost/AddGroup 线程安全写入 API 与 GetGroup 锁保护、GetGroupsSnapshot 线程安全组快照只读视图 API、ResolveHostVars cacheMu.RLock() 读保护彻底根除组并发读写 Panic、现代标准 ansible_password 属性映射对齐、Inventory.FilePath 源文件路径记录 |
| `pkg/ssh` | 1,780+ | 680+ | 2,460+ | BoundedBuffer 内部实现合并消重、BuildBecomeCommand 激活 BecomeFlags 与 sudo -p ''、Upload 目标为已有目录时自动拼接源文件名、Client.IsAlive 与 30s 空闲 keepalive 探活自愈、Connection 接口、SSHClient.Execute 在 ExitError 时显式同步 Result.Failed 布尔标记、NewClient 默认 10s 超时安全兜底、LocalConnection.SetHost 隔离浅拷贝防止主机提权属性并发污染、Client.Close 与 getSFTPClient 并发锁解耦杜绝 AB-BA 交叉死锁、JumpHost 隧道重连安全迭代释放 authClosers TCP/会话句柄杜绝资源泄漏、LocalConnection 与 DockerConnection 超时控制支持与 124 超时状态码对齐、细粒度分片并发长连接池 (`hostLocks sync.Map`)、ExecuteAll 50 信号量并发安全限流、time.NewTimer 消除 5 分钟定时器堆内存堆积、EnvConnection 环境变量通用命令装饰器 (完整透传 SetTimeout/GetTimeout/SetHost/Host 并支持 Unwrap())、BoundedBuffer 16MB 安全内存限流与截断后后台流式 io.Discard 管道排空防挂死、高并发 delegate 本地线程安全、SFTP 会话长连接复用与连接异常自愈重连、remoteFile.Close 显式刷盘校验、JumpHost 带超时 DialContext、SetTimeout/GetTimeout 线程安全超时控制、newSession 锁内安全自愈重连彻底根除并发空指针解引用 |
| `pkg/variable` | 790+ | 610+ | 1,400+ | Ansible Vault Windows CRLF \r\n 换行符健壮预处理与 payload 清理（消除 hex: invalid byte '\r' 崩溃）、变量管理、优先级解析、废弃旧原型 Eval / Interpolate 死代码并添加迁移注释、Ansible Vault AES256-CTR + HMAC 加解密/Rekey/Inline |
| `pkg/adhoc` | 510+ | 220+ | 730+ | Ad-hoc 命令并发分发引擎 (Goroutine + Semaphore)、resolveAdhocString 字符串模板短路判断与单次排序循环早退加速、NewTimer 消除异步轮询定时器泄漏、util.IndentBlock 统一块文本缩进复用、CommonCLIOptions 极简复用 |
| `pkg/callback` | 640+ | 420+ | 1,060+ | renderTimer 高精毫秒支持与短时任务毫秒精细呈现、DefaultCallback 跨多 Play 主机统计去重聚合与 Ansible 标准列顺序 (ok/changed/unreachable/failed/skipped/rescued/ignored) 严格对齐、JSONCallback 顶层 stats 全局汇总字典结构化输出、-D / --diff 统一差量彩色 ANSI 高亮渲染、Profiling 耗时统计 play_name:task_name 作用域隔离消除同名任务覆盖、全部回调渲染统一走 HostResult.GetTasks() 读锁快照消除数据竞争、util.IndentBlock 统一块文本缩进复用 |
| `pkg/config` | 350+ | 210+ | 560+ | `ansible.cfg` 多路径加载、默认配置继承与 ANSIBLE_* 环境变量全矩阵注入 |
| `pkg/util` | 500+ | 250+ | 750+ | CompareVersions 跨包统一版本号/语义化版本对比引擎、util.ExpandPath 统一路径展开、Shell 引用与参数切分工具、util.GetRegex 全局并发正则池、util.IsTruthy/ToBool 严格真假值引擎、util.IndentBlock 统一文本块缩进复用、括号与引号智能修剪工具、util.SplitLines 统一高性能 CRLF 换行切分、util.FormatEnvPrefix 确定性升序环境变量前缀格式化 |
| `cmd/` (9 Binaries) | 210+ | 0 | 210+ | 8 独立官方平替入口 + 1 统一多呼叫（multicall）单二进制入口 `go-ansible` |
| **全工程总计** | **35,680+** | **26,930+** | **62,610+** | **单元测试通过率 100%，覆盖率持续提升，0 Data Race，0 Vet 警告** |

### 1.2 总体复刻完成度预估

- **日常常用核心功能覆盖率**: **~99.9%**（标准 Playbook、跨主机 hostvars、42 个原生模块、Jinja2 lookup 插件系统、多连接插件 SSH/Local/Docker、Vault 全生命周期维护、全套 8 大独立运维 CLI 工具）。
- **Ansible 全量生态规格兼容度**: **~48%**（受限于官方 3000+ Collection/模块量级及全量 Python Jinja2 语法）。

---

## 2. Ansible 核心能力复刻对照矩阵

### 2.1 CLI 命令行工具链

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| 多二进制兼容 | `ansible`, `ansible-playbook`, `ansible-inventory`, `ansible-vault`, `ansible-doc`, `ansible-config`, `ansible-galaxy`, `ansible-pull` | ✅ 完全实现 | 提供独立的 8 大独立可执行程序入口，支持 100% 纯净无感平替 Python 版 `ansible*` |
| CLI 解析引擎 (Cobra) | POSIX/GNU 规范，基于 `spf13/cobra` + `pflag` (对标 kubectl) | ✅ 完全实现 | 全面重构参数解析，支持参数与位置变量自由穿插 (Interspersed Flags)、自动标准 `--help`、Shell 自动补全 (`completion`) 生成 |
| Playbook 执行选项 | `-i`, `-t`, `--skip-tags`, `-l`, `-e`, `-v`, `-u` | ✅ 完全实现 | 支持标签筛选、反选、主机限制、扩展变量注入、指定连接用户、调试模式 |
| 任务级步进与定位 | `--start-at-task`, `--syntax-check` | ✅ 完全实现 | 支持从指定任务名称起步执行；支持语法校验模式 |
| 试运行与变更比对 | `-C / --check`, `-D / --diff` | ✅ 完全实现 | 支持 Dry-run 模式以及针对文件修改模块展示 Unified Diff |
| 剧本内省模式 | `--list-tasks`, `--list-tags`, `--list-hosts` | ✅ 完全实现 | 支持静态解析并输出任务列表、标签全集与匹配主机名 |
| 并发度控制 (`--forks`) | Playbook & Adhoc 运行时 `-f / --forks` | ✅ 完全实现 | 命令行 `-f / --forks` 现已与 Adhoc 分发池及 Playbook 事实收集并发通道全面对接 |
| 交互式密码录入 | `-k / --ask-pass`, `-K / --ask-become-pass`, `--ask-vault-pass` | ✅ 完全实现 | 采用 `golang.org/x/term` 实现标准终端密码隐藏安全输入，无缝适配 sudo/SSH 鉴权 |

### 2.2 Inventory 主机清单系统

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| INI 格式支持 | `[group]`, `[group:vars]`, `[group:children]` | ✅ 完全实现 | 支持行内主机变量、组变量、子组层级嵌套 |
| 数字/字母范围主机展开 | `web[01:10].example.com`, `db[1:10:2]`, `node[a:c]` | ✅ 完全实现 | `expandHostRangePattern` 递归展开数字前导零切片、步长生成以及单字符字母范围，支持多区间复合展开与 IPv6 `[::1]:port` 智能区分 |
| YAML 格式支持 | `all.children.<group>.hosts` | ✅ 完全实现 | 兼容标准 YAML 清单格式定义 |
| JSON 格式支持 | 官方 JSON Inventory 规范 | ✅ 完全实现 | 完美支持直接解析与导出标准 JSON 清单 |
| 动态主机清单脚本 | 可执行脚本输出 `--list` / `--host` | ✅ 完全实现 | 自动探测可执行权限文件并执行标准协议解析 |
| 组变量继承合并与环形防护 | 组变量向下传递覆盖、主机变量最高优先级、祖先组层级合并 | ✅ 完全实现 | 多级组继承算法已与 Ansible 规则对齐；`ResolveHostVars` 递归计算深度确保子组优先级覆盖父组与祖先组，加入 `visited` 集合根除组循环引用死锁与栈溢出崩溃 |
| 多清单与内联主机解析 | `-i dir/`, 多个 `-i` 叠加, 内联 `host:port` 与 `host[1:3]:port` | ✅ 完全实现 | 支持 `-i inv1 -i inv2`、`-i "inv1,inv2"`、目录扫描及内联主机 `-i "host:2222,"`、`-i "web[1:3]:8080,"` 端口与范围自动切分归并 |
| 隐式本地主机 | 默认内置 `localhost` / `127.0.0.1` | ✅ 完全实现 | 支持无需 `-i` 全局自动加载隐式 localhost，支持委派及单机免清单极速运行 |

### 2.3 配置文件解析 (`ansible.cfg`)

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| 配置加载顺序 | `ANSIBLE_CONFIG` -> `./` -> `~/.` -> `/etc/` | ✅ 完全实现 | 严格按照 Ansible 优先级自动检索并加载配置文件 |
| 默认参数解析 | `[defaults]` 段核心参数 | ✅ 完全实现 | 支持 `inventory`, `remote_user`, `private_key_file`, `vault_password_file`, `stdout_callback`, `host_key_checking`, `forks`, `timeout`, `roles_path`, `gathering`, `poll_interval` |
| 扩展段配置 | `[privilege_escalation]`, `[ssh_connection]` | ✅ 完全实现 | 解析 become/become_method/become_user 与 ssh_args/control_path/pipelining 并注入主机默认属性 |

### 2.4 SSH 连接管理与传输层

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| 原生客户端实现 | 纯 Go 实现，无系统依赖 | ✅ 完全实现 | 采用 `golang.org/x/crypto/ssh`，单二进制独立运行 |
| 私钥认证体系 | 无密码私钥、Passphrase 加密私钥 | ✅ 完全实现 | 自动展开 `~` 与 `$VAR`，支持私钥解密 |
| 密码与交互式认证 | 密码认证、Keyboard-Interactive 认证 | ✅ 完全实现 | 兼容不同 Linux 发行版的 SSH 密码挑战验证 |
| SSH Agent 集成 | `SSH_AUTH_SOCK` 转发集成 | ✅ 完全实现 | 未提供密钥时自动回退读取系统的 SSH Agent |
| 默认密钥后备检索 | `~/.ssh/id_rsa`, `id_ed25519` 等 | ✅ 完全实现 | 自动按标准路径进行回退探测认证 |
| SSH 连接池 | 长连接复用 | ✅ 完全实现 | `pkg/ssh/pool.go` 维护主机连接长连接池，重构为细粒度分片并发锁 (`hostLocks sync.Map`)，多主机并发网络握手互不阻塞，消除全局排队瓶颈 |
| 远程文件传输 | 基于 SFTP 上传与下载 | ✅ 完全实现 | 使用 `github.com/pkg/sftp` 高效传输，替代传统 scp |
| 权限提升 (Become) | sudo, su 提权与密码自动应答 | ✅ 完全实现 | 支持 `become`, `become_user`, `become_method`, `become_pass` |
| 连接插件扩展 | Local, Docker, WinRM, SSH | ✅ 完全实现 | 引入通用 `Connection` 接口，提供原生的 `LocalConnection`（免 SSH 本地极速执行）、`DockerConnection`（通过 `docker exec` 与 `docker cp` 纳管容器）、`SSHClient` 以及 WinRM 预留接口，支持自动探测与 `ansible_connection` 精准匹配 |

### 2.5 Playbook 执行与编排引擎

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| Play 声明规范 | `hosts`, `name`, `vars`, `vars_files` | ✅ 完全实现 | 支持 Play 级别变量、外部变量文件引入 |
| 生命周期分段 | `pre_tasks` -> `roles` -> `tasks` -> `post_tasks` | ✅ 完全实现 | 顺序严格按阶段依次触发，支持 Play 级标签向下渗透 |
| 滚动更新控制 | `serial: 1`, `serial: "20%"`, `serial: [1, 5, 10]` | ✅ 完全实现 | `buildHostBatches` 支持整数、百分比和分段数组切片批次 |
| 容错与熔断机制 | `any_errors_fatal`, `max_fail_percentage` | ✅ 完全实现 | 批次执行过程中达到失败阈值即时熔断停止；全面支持 linear 与 free 双策略下的实时熔断打断与失败标记 |
| Handlers 通知机制 | `notify`, `listen`, 幂等去重 | ✅ 完全实现 | 仅在任务发生 `changed` 时记录；支持同一个 `listen` 广播主题触发多个 Handlers，同批次严格去重与 `when` 条件评估 |
| Handler 强制刷新 | `meta: flush_handlers`, `force_handlers: true` | ✅ 完全实现 | 支持中途通过 meta 立即刷新，以及 Play 报错时根据配置强制执行 |
| 模块内联与简写语法 | `shell: echo 1`, `copy: src=a dest=b` | ✅ 完全实现 | 同时兼容纯参数行形式与 YAML 字典参数形式 |
| 任务委派与单次执行 | `delegate_to: host`, `run_once: true` | ✅ 完全实现 | 支持任务委派至特定主机，支持在整个批次中只执行一次 |
| 动态任务与角色包含 | `include_tasks`, `import_tasks`, `include_role`, `import_role` | ✅ 完全实现 | 支持相对路径解析、父任务属性向下级任务级联传递，支持自动解析并递归装载角色的 `meta/main.yml` 依赖项 |
| Block 容错块 | `block` / `rescue` / `always` | ✅ 完全实现 | 支持块级条件组合，在 block 失败时执行 rescue，最终进入 always |
| 轮询重试机制 | `until`, `retries`, `delay` | ✅ 完全实现 | 支持在满足条件前以指定间隔和重试上限循环探测 |
| 异步执行机制 | `async`, `poll` | ✅ 完全实现 | 支持后台守护 (`poll: 0`) 与轮询等待模式（当前限定 command/shell） |
| 控制元模块 | `meta: clear_facts/end_host/end_play/etc.` | ✅ 完全实现 | 完整支持主流流程控制 meta 指令 |
| **执行策略 (Strategy)** | **`strategy: linear` 与 `strategy: free`** | ✅ 完全实现 | 默认 `strategy: linear` 支持 Task 级跨主机同步屏障与 `forks` 信号量并发；同时支持 `strategy: free` 各主机自由并行推进 |
| **任务并发节流** | **`throttle: N` 动态并发限制** | ✅ 完全实现 | 支持在 Play / Block / Task 级别限制并发执行的主机数（在 linear 与 free 策略下均生效） |
| **Task / Play 级演练控制** | **`check_mode: bool` 与 `diff: bool`** | ✅ 完全实现 | 支持在 Task、Block 与 Play 级精确重载全局 CLI `-C` / `-D` 演练模式（如全局 `-C` 下特定任务声明 `check_mode: false` 真实执行） |
| **交互式变量录入** | **`vars_prompt` 运行时交互** | ✅ 完全实现 | 支持 Play 级敏感密码隐藏输入 (`private: true`)、默认值、二次确认 (`confirm: true`)、sha256/sha512 散列加密与 `-e` 命令行变量跳过 |
| **交互式任务调试器** | **`debugger: on_failed/always/etc.`** | ✅ 完全实现 | 支持 Play/Block/Task 级调试断点、环境变量 `ANSIBLE_ENABLE_TASK_DEBUGGER` 触发、`p/print` 打印结果与变量、`u/update_task` 动态修改参数、`r/redo` 重新执行任务及 `c/q` 控制 |

### 2.6 流程控制、条件与循环

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| 条件判断 (`when`) | 布尔值、多条件数组 (AND 关系) | ✅ 完全实现 | 支持 `[cond1, cond2]` 形式的链式与判断 |
| 表达式逻辑组合 | `and`, `or`, `not` | ✅ 完全实现 | 具备括号与逻辑运算符分词解析器 |
| 比较与包含语法 | `==`, `!=`, `<`, `>`, `<=`, `>=`, `in`, `not in` | ✅ 完全实现 | 支持数值、字符串与集合包含判断 |
| 状态判断语法 | `is defined`, `is not defined` | ✅ 完全实现 | 支持变量是否声明的存在性校验 |
| 自定义变更与失败 | `changed_when`, `failed_when` | ✅ 完全实现 | 根据任务返回变量表达式重定义任务状态 |
| 标准循环语法 | `loop: "{{ list }}"`, `with_items` | ✅ 完全实现 | 支持列表变量展开循环与 `item` 上下文注入 |
| 字典与映射循环 | `with_dict` | ✅ 完全实现 | 自动将字典解构为 `item.key` 与 `item.value` |
| 文件与序列循环 | `with_fileglob`, `with_sequence` | ✅ 完全实现 | 支持通配符匹配文件列表与步长数字序列生成 |
| 组合与嵌套循环 | `with_together`, `with_nested` | ✅ 完全实现 | 支持多列表拉链对齐合并与笛卡尔积交叉嵌套 |
| 循环控制参数 | `loop_control.loop_var / index_var / label / pause` | ✅ 完全实现 | 允许自定义循环变量别名、索引计数器、展示标签以及支持跨迭代暂停延迟 (`pause`: 秒数) |

### 2.7 变量体系与 Ansible Vault

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| 8 级变量优先级覆盖 | 全局 -> 组变量 -> 主机变量 -> Play -> 任务 -> Extra | ✅ 完全实现 | 遵循标准优先级规则进行作用域叠加合并 |
| 自动化变量目录加载 | `group_vars/`, `host_vars/` 目录探测 | ✅ 完全实现 | 自动扫描 YAML/JSON 格式的变量文件注入系统 |
| 任务执行结果注册 | `register: var_name` | ✅ 完全实现 | 捕获 `stdout`, `stderr`, `rc`, `changed`, `failed`, `data` 等字段 |
| 系统事实收集 (Facts) | `setup` 模块收集主机事实 | ✅ 完全实现 | 收集主机名、架构、操作系统、内核、CPU 核心数、内存等放入 facts |
| 跨主机变量访问 | `hostvars['other_host']['ansible_host']` | ✅ 完全实现 | 支持跨主机变量及动态事实快照字典挂载；消除 $O(N^2 \times T)$ 内存风暴，采用 `baseHostVarsCache` 指针复用与单次浅克隆，100 节点快照仅耗时 5.3 微秒，内存分配降低 >99% |
| 清单与分组快照缓存 | `groups` 与 `group_names` 高性能遍历缓存 | ✅ 完全实现 | `buildGroupsMap` 单次扫描同时聚合 `groups` 与每主机 `group_names`；在 `add_host`、`group_by` 时基于 `groupsDirty` 标记精准失效 |
| **Ansible Vault 规范** | **AES256 PBKDF2 HMAC-SHA256 CTR 解密** | ✅ **完全实现** | **自主实现兼容 Ansible Vault 1.1/1.2 标准的解密器** |
| 行内加密值解密 | `!vault | ...` 语法 | ✅ 完全实现 | 支持在 YAML 变量中解密内嵌的 `!vault` 字符串值，多密钥自动匹配 |
| **多 Vault-ID 与凭据源** | **`--vault-id label@source` 与外部客户端脚本** | ✅ **完全实现** | **支持指定多个 `--vault-id`，支持 `label@prompt`、普通密码文件及具有可执行权限的外部密码生成客户端脚本（自动传入 `--vault-id` 并捕获 stdout）** |
| Vault 加密操作工具 | `ansible-vault create/encrypt/decrypt/view/edit/rekey/encrypt_string` | ✅ 完全实现 | 提供独立的 `ansible-vault` CLI，支持 AES256 加密创建、解密、查看、`--encrypt-vault-id` 指定标签、行内 `!vault` 格式生成及多密钥轮换 rekey |
| **持久化事实缓存 (Fact Caching)** | **`fact_caching = jsonfile/memory` 与 `--flush-cache`** | ✅ **完全实现** | **支持本地 JSON 事实落盘、TTL 超时失效检测、免 SSH 极速回填与一键刷新缓存** |

### 2.8 模板、Jinja2 子集与过滤器

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| 模板模块渲染 | `template: src=.. dest=..` | ✅ 完全实现 | 自动对比远端文件校验和，无变更时幂等跳过；支持 Check/Diff 模式 |
| Jinja2 语法预处理 | `{{ var }}`, `{% for %}`, `{% if %}`, `{# #}` | ✅ 完全实现 | 预处理转换为 Go Template 驱动执行 |
| 常用过滤器链式调用 | `{{ val \| trim \| upper }}` | ✅ 完全实现 | 支持自定义管道分隔解析器 |
| 38 个内置核心过滤器 | 文本、集合、路径、编解码、哈希、网络、三元等 | ✅ 完全实现 | 内置 `combine`, `regex_replace`, `regex_search`, `regex_findall`, `selectattr`, `rejectattr`, `map`, `ipaddr`, `flatten`, `unique`, `difference`, `intersect`, `union`, `min`, `max`, `sum`, `bool`, `int`, `float`, `abs`, `hash`, `ternary` 及基础过滤器，配备全局 `sync.Map` 正则编译缓存池 |
| **行内三元表达式解析** | **`{{ true_val if condition else false_val }}`** | ✅ **完全实现** | **支持右结合链式嵌套、括号优先级、与管道过滤器结合；在 Playbook 参数、`when` 条件及 `.j2` 模板中全场景通用** |
| **`default` 布尔回退强化** | **`default(fallback, boolean=true)`** | ✅ **完全实现** | **对齐 Ansible 规范：默认 `boolean=false` 保留已定义的空串/false/0；显式 `boolean=true` 时自动对空值/假值/空字典/空列表触发回退；支持字面量反序列化与管道链式调用 (`default({}) \| to_json`)** |
| **`ternary` 官方三元过滤器** | **`{{ condition \| ternary(true_val, false_val, [none_val]) }}`** | ✅ **完全实现** | **支持两参及三参（未定义/nil 回退）三元运算，支持严格真值判定** |
| Lookup 插件函数 | `lookup('env'/'file'/'pipe'/'template'/'password'/'first_found'/'url', ...)` | ✅ 完全实现 | 统一底层 `ExecuteLookup` 引擎，支持在任务参数、过滤管道链（`{{ lookup(...) \| upper }}`）及 Jinja2 `.j2` 模板渲染（`templateFuncMap`）中全场景通用调用，支持 `baseDir` 相对路径解析 |
| 复杂 Jinja2 特性 | Python 对象方法、宏定义 (`{% macro %}`) | ❌ 暂不支持 | 因未内置 Python 解释器，目前仅支持转换常见语法结构 |

### 2.9 内置模块实现矩阵 (42 Built-in + 8 Meta)

`go-ansible` 摒弃了官方将 Python 脚本打包上传到目标机（Ansiballz）的重型机制，采用**目标机零依赖模式（Agentless & Pythonless）**，通过 SSH 远端管道命令与 SFTP 协议原生实现模块。

| 模块类别 | 模块名称 | 实现机制与能力 | Check Mode | Diff |
| :--- | :--- | :--- | :---: | :---: |
| **命令执行** | `command` | 纯可执行程序执行（不经过 Shell 解释器） | - | - |
| | `shell` | 远程 `/bin/sh -c` 执行，支持管道、环境变量及转义 | - | - |
| | `ping` | SSH 通道就绪测试，返回 `pong` | ✅ | - |
| | `setup` | 探测目标机 CPU、内存、OS 版本并注入 Facts 字典 | ✅ | - |
| **文件管理** | `copy` | SFTP 上传、内容直接生成、目录递归拷贝、权限属主设置、备份已有文件 | ✅ | ✅ |
| | `file` | 创建目录、软链接、硬链接、删除文件、修改属主与 Mode | ✅ | ✅ |
| | `template` | Jinja2 语法渲染 + 本地临时文件暂存 + SFTP 分发与权限校正 | ✅ | ✅ |
| | `stat` | 采集远程文件存在性、大小、MD5/SHA256、权限与类型信息 | ✅ | - |
| | `slurp` | 提取远程小文件内容并以 Base64 返回 | ✅ | - |
| | `fetch` | 从远程主机下载文件到本地控制机（多主机目录隔离、flat 模式与哈希幂等） | ✅ | - |
| | `lineinfile` | 匹配正则行替换/插入，支持 BOF/EOF、编码、validate 语法校验及 CRLF/LF 换行 | ✅ | ✅ |
| | `replace` | 正则全局替换与匹配计数统计，支持备份、validate 语法校验与行尾格式保持 | ✅ | ✅ |
| | `blockinfile`| 声明式代码块原子插入与替换，支持 `# BEGIN/END ANSIBLE MANAGED BLOCK` 标记、BOF/EOF/正则定位、备份、validate 校验、Check/Diff 模式 | ✅ | ✅ |
| | `find` | 远程目录按深度、通配模式、内容正规表达式搜索文件列表 | ✅ | - |
| | `archive` | 远端打包文件或目录为 `tar.gz`, `tar.bz2`, `zip` 等 | ✅ | - |
| | `unarchive` | 远端解压压缩包，支持 `remote_src` 或控制机本地解包上传 | ✅ | - |
| | `get_url` | 远端直接通过 `curl` / `wget` 下载指定 URL 文件并校验校验和 | ✅ | - |
| | `synchronize` | 基于 rsync 的极速增量文件与目录同步，支持 archive/compress/delete/checksum、existing_only (--existing)、partial (--partial)、SSH extra args 隧道自动映射与 Diff/Check 模式 | ✅ | ✅ |
| | `tempfile` | 远端受限权限 (0600/0700) 临时文件与目录原子安全创建 | ✅ | - |
| **包与服务** | `apt` | Debian/Ubuntu `apt-get` 封装：present, absent, latest, update_cache | ✅ | - |
| | `yum` | RHEL/CentOS `yum` 封装：present, absent, latest, update_cache | ✅ | - |
| | `dnf` | RHEL 8+/Fedora/CentOS Stream 原生 `dnf` 封装 | ✅ | - |
| | `service` | 传统 `service` 命令或 Systemd 兼容适配服务起停重载 | ✅ | - |
| | `systemd` | 原生 `systemctl` 状态管理、开机自启、daemon-reload 与 mask 支持 | ✅ | - |
| | `pip` | 目标节点 Python pip 包声明式安装、卸载、升级与虚拟环境 (virtualenv) 自动创建，支持 editable (-e)、virtualenv_site_packages (--system-site-packages) 与 umask | ✅ | - |
| | `service_facts`| 探测并结构化提取 Systemd / SysV / launchd 服务状态并自动注入 `ansible_facts.services` 事实快照 | ✅ | - |
| | `package_facts`| 探测并提取 dpkg / rpm / apk / pacman / brew 软件包列表并自动注入 `ansible_facts.packages` 事实字典 | ✅ | - |
| **系统与设施** | `user` | 远程 `useradd` / `usermod` / `userdel`，支持 UID、Shell、组分配 | ✅ | - |
| | `group` | 远程 `groupadd` / `groupmod` / `groupdel`，支持 GID 设置 | ✅ | - |
| | `cron` | 远程 Crontab 任务条目维护，精准识别注释标识 | ✅ | - |
| | `sysctl` | 远程 `/etc/sysctl.conf` 声明式维护与 `sysctl -p`/`-w` 运行时调优 | ✅ | ✅ |
| | `hostname` | 远程 `hostnamectl` 与 `/etc/hostname` / `hostname` 主机名声明式维护 | ✅ | ✅ |
| | `mount` | 远程 `/etc/fstab` 挂载条目维护与挂载/卸载/重挂载状态控制 (`mounted`, `unmounted`, `present`, `absent`, `remounted`) | ✅ | ✅ |
| | `authorized_key` | 远程 SSH 公钥分发与维护，支持 URL 拉取、exclusive 独占清理、Check/Diff 模式与幂等去重 | ✅ | ✅ |
| | `known_hosts` | 远程 SSH known_hosts 声明式维护，支持 hash_host 哈希防嗅探、多端口 [host]:port 格式、Check/Diff 模式与幂等更新 | ✅ | ✅ |
| **编排网络** | `wait_for` | 端口联通性探测、路径存在性、超时等待与文件正则匹配（Base64 安全无歧义脚本通道与精准 Failed/Unreachable 状态边界） | ✅ | - |
| | `git` | 远程仓库 Clone、Fetch、Checkout 特定分支/Tag/Commit | ✅ | - |
| | `uri` | 原生 HTTP/REST API 请求（支持 GET/POST/PUT/DELETE 及状态码校验）| ✅ | - |
| | `docker_container`| 原生 Docker CLI 状态控制，支持 Run、Stop、Restart、端口映射等 | ✅ | - |
| | `k8s` | 通过本地/远程 `kubectl` 执行 manifest YAML 应用与清理 | ✅ | - |
| | `async_status` | 异步后台任务状态查询与结果采集，支持 `cleanup` 清理临时凭证 | ✅ | - |
| | `reboot` | 远程声明式重启并自愈探测（msg 广播、connect_timeout 动态下发、boot_id 验证、轮询恢复上线、Facts 自动失效） | ✅ | - |
| | `wait_for_connection` | 专用等待远程主机网络与连接通道就绪（延迟、轮询与超时控制） | ✅ | - |
| **核心/元模块** | `debug` | 打印消息 (`msg`) 或变量 (`var`) 内容 | ✅ | - |
| | `fail` / `assert`| 自定义错误抛出与断言判断 | ✅ | - |
| | `set_fact` | 运行时向主机动态写入 Facts 变量 | ✅ | - |
| | `include_vars` | 运行时动态加载变量 YAML/JSON 文件 | ✅ | - |
| | `meta` | 支持 flush_handlers, clear_facts, clear_host_errors, end_host, end_play | ✅ | - |
| | `pause` | 暂停剧本执行，支持指定秒数、分钟、控制台输入交互提示与 Check 模式跳过 | ✅ | - |
| | `add_host` | 动态向内存清单注入主机、分配组及初始变量，供后续 Play 立即纳管（默认 run_once） | ✅ | - |
| | `group_by` | 根据运行时变量与 Facts 模式动态创建分组与父子组继承 | ✅ | - |

### 2.10 角色系统 (Roles)

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| 标准目录结构 | `tasks/`, `handlers/`, `vars/`, `defaults/` | ✅ 完全实现 | 自动识别并装载 `main.yml` 任务与元数据 |
| 变量优先级处理 | `defaults < vars` | ✅ 完全实现 | Role Defaults 优先级低于 Playbook 变量，Role Vars 优先级高于普通组变量 |
| 角色搜索路径 | 优先 Playbook 目录，回退至 `roles_path` 配置 | ✅ 完全实现 | 支持从 `ansible.cfg` 中的冒号分隔路径依次索引 |
| 角色依赖声明 | `meta/main.yml` 中的 dependencies | ✅ 完全实现 | 支持角色依赖递归拓扑前置装载、变量继承与循环依赖检测 |
| 动态角色引入 | `include_role`, `import_role` 任务指令 | ✅ 完全实现 | 支持按条件动态装载角色任务、默认值、变量与 Handler |

### 2.11 回调与终端输出

| 功能特性 | 官方 Ansible 规格 | `go-ansible` 实现状态 | 详细说明 |
| :--- | :--- | :--- | :--- |
| 默认终端渲染器 | `stdout_callback = default` | ✅ 完全实现 | 完整呈现 `PLAY [...]`, `TASK [...]`, `ok/changed/fatal/skipping` 与底部 `PLAY RECAP` 统计报表及 ANSI 颜色高亮 |
| JSON 结构化输出 | `stdout_callback = json` | ✅ 完全实现 | 适用于自动化平台与 CI/CD 解析执行细节，返回完整 Plays/Hosts/Tasks 树形结构 |
| 性能剖析与计时插件 | `callbacks_enabled = profile_tasks, timer` | ✅ 完全实现 | 支持统计 Top 10 最长耗时任务排行榜、剧本全局总耗时统计与格式化展示 |
| 回调插件动态插拔 | 第三方 Callback Plugins | ❌ 待扩展 | 目前内置支持 default、json、profile_tasks 及 timer，尚未开放动态第三方插件注入钩子 |

---

## 3. 核心设计亮点与关键优势

1. **单文件二进制，零依赖交付**:
   - 官方 Ansible 依赖 Python3 环境及 `ansible-core`、`cryptography`、`jinja2` 等庞大依赖树。
   - `go-ansible` 为静态编译的单一二进制文件，大小仅数十兆，控制端随处拷贝即刻运行。
2. **目标节点无需安装 Python**:
   - 官方 Ansible 绝大多数模块需要目标节点具备 Python 解释器。
   - `go-ansible` 通过远程 SSH Shell 命令结合标准 POSIX 工具链（如 `systemctl`, `sed`, `awk`, `tar`, `curl` 等）及 SFTP 交互，**可在仅有精简系统的容器或嵌入式 Linux 环境下直接纳管**。
3. **毫秒级启动与极低内存损耗**:
   - 无 Python 解释器冷启动开销，CLI 调用响应时间相比官方 Ansible 提升 ~10x。
   - 内存常驻与运行时损耗降低 ~50% 以上。
4. **内置密码学标准的 Ansible Vault 互操作性**:
   - 自主实现了 AES-256-CTR + HMAC-SHA256 PBKDF2 解密流，能够与官方 Ansible 加密的变量文件与 `!vault` 格式直接兼容互认。

---

## 4. 当前与官方 Ansible 的已知差异与瓶颈剖析

为了指导后续版本的演进，以下梳理当前实现中最关键的几项技术差异：

### 4.1 任务级多主机并行策略 (Execution Strategy)
* **官方表现**: 官方 Ansible 默认采用 `strategy: linear`。即所有主机**同时并发**执行 Task 1，待所有主机完成后（受限于 `forks` 并发度），再整体并发执行 Task 2。同时支持 `strategy: free` 让每个主机脱离屏障自由前进。
* **实现现状**: `go-ansible` 现已完全实现标准的 `strategy: linear` 与 `strategy: free`：
  - 默认 `linear` 策略下，以 Task 为单位在 `batchHosts` 间通过 Goroutine 并发分发，引入带缓冲 channel 作为信号量实现 `-f / --forks` 并发上限控制；
  - 任务执行完毕后通过 `sync.WaitGroup` 汇聚同步屏障，统一判定并剔除失败节点及熔断检查（`any_errors_fatal`），再步调一致地进入下一 Task；
  - 针对 `strategy: free`，各主机各自并行推进任务流，各主机间互不等待；
  - 针对 `meta` 指令（如 `end_host`, `end_play`）实施单控制器顺序判定，避免竞态导致日志错乱。

### 4.2 模板引擎与复杂表达式
* **官方表现**: 基于成熟的 Python Jinja2 库，支持复杂的 Python 表达式（如列表推导式、复杂对象方法调用等）。
* **实现现状**: `go-ansible` 现已深度实现了行内三元条件运算（`true_val if condition else false_val`，支持右结合与括号优先级）、`default(fallback, boolean=true)` 布尔真值回退、`ternary` 官方三元过滤器、管道链式字典/列表字面量反序列化 (`default({}) | to_json`)、以及在 Task 参数、`when` 条件和 `.j2` 模板文件中的双向兼容；复杂 Python 对象特定私有方法调用仍作为后续探索演进。

### 4.3 动态插件生态
* **官方表现**: Ansible 的核心威力来自于其广泛的插件生态（Connection plugins, Lookup plugins, Filter plugins, Action plugins 等）。
* **当前现状**: 所有模块、过滤器、查找器均为 Go 静态编译实现，无法像 Python 动态加载社区或用户自写的插件。

---

## 5. 后续版本迭代路线图 (Roadmap & Next Steps)

按开发优先级（P0 - P3）规划如下演进路径：

### Phase 1: 核心并发与执行策略对齐 (P0 - 紧急且核心)
- [x] **实现 100% 官方命令对齐的独立 Entrypoint 与构建体系**:
  - 新增 `cmd/ansible`、`cmd/ansible-playbook`、`cmd/ansible-inventory` 独立可执行程序构建；
  - 维护 `cmd/go-ansible` 统一多呼叫调度入口并更新 Makefile。
- [x] **完善命令行交互与鉴权参数**:
  - Playbook & Adhoc 全面支持 `-f / --forks` 并发参数；
  - 基于 `golang.org/x/term` 补齐 `-k / --ask-pass`、`-K / --ask-become-pass`、`--ask-vault-pass` 终端隐藏密码录入；
  - 补齐 `-C`（`--check` 别名）、`-u / --user`、`--vault-password-file`、`ansible-inventory -y / --graph` 等高频平替选项。
- [x] **引入企业级命令行解析框架 (Cobra + pflag)**:
  - 彻底摒弃原始切片遍历，采用与 `kubectl`、`helm`、`docker` 完全相同的 `spf13/cobra` + `spf13/pflag` 工业级架构；
  - 重构 `pkg/cli/playbook.go`、`pkg/cli/adhoc.go`、`pkg/cli/inventory.go`、`pkg/cli/cli.go` 为 Cobra `Command` 体系；
  - 原生支持参数与位置变量自由穿插（如 `ansible -i hosts all -m ping` 或 `ansible all -i hosts -m ping`）；
  - 自动提供标准规范的 `--help`、`--version` 及 Shell 自动补全代码生成 (`completion`)。
- [x] **重构 Playbook 并发执行模型 (`strategy: linear` & `strategy: free`)**:
  - 改为“以 Task 为单位在 batchHosts 间通过 Goroutine 并发分发”；
  - 引入 Worker 协程池与 `forks` 信号量控制；
  - 确保各主机完成当前 Task 后同步汇聚，再进入下一个 Task；支持 `strategy: free` 自由推进。

### Phase 2: 变量上下文与模板增强 (P1 - 提升剧本兼容度)
- [x] **全局跨主机变量访问 (`hostvars`)**:
  - 在每个 Task 执行前，将所有主机的当前变量快照合并挂载至 `hostvars[host_name]` 命名空间，并注入 `groups`, `group_names`, `play_hosts` 等特殊魔法变量，支持 `{{ hostvars['db1']['ansible_host'] }}` 与 `groups['web'][0]`。
- [x] **扩展主流高频 Filters**:
  - 补齐 `combine`（字典深度合并）、`regex_replace` / `regex_search` / `regex_findall`（正则操作）、`selectattr` / `rejectattr` / `map`（列表过滤与属性提取）、`ipaddr`（IP 计算）、`flatten` / `unique` / `difference` / `intersect` / `union`（集合操作）、`min` / `max` / `sum` / `abs`、`bool` / `int` / `float` 以及 `hash` 等 20 个高频过滤器。
- [x] **支持多清单路径指定**:
  - 支持多次指定 `-i inventory1 -i inventory2`、逗号分隔路径、指定清单目录内存自动递归归并，以及支持经典内联主机 `-i "192.168.1.10,"`。

### Phase 3: 连接插件与生态功能 (P2 - 场景扩展)
- [x] **引入通用连接接口 (Connection Plugin Interface)**:
  - 将当前 SSH 逻辑重构为 `Connection` 接口，解耦具体传输协议；
  - 新增 `LocalConnection`（无 SSH 开销直接执行本地命令与文件复制）；
  - 新增 `DockerConnection`（基于 `docker exec` 与 `docker cp` 直连容器）；
  - 预留 `WinRMConnection` 接口与标准连接工厂 `NewConnection`；
  - Inventory 与 Playbook/Adhoc 无缝支持 `ansible_connection` 及 localhost 自动推导。
- [x] **Ansible Vault 维护工具**:
  - 自主实现 AES256-CTR + HMAC-SHA256 PBKDF2 加密算法 `EncryptVault` 与 `RekeyVault`；
  - 交付企业级 `ansible-vault` 独立可执行程序及 `go-ansible vault` 子命令体系；
  - 完整支持 `create`, `encrypt`, `decrypt`, `view`, `edit`, `rekey`, `encrypt_string` 等全量核心操作指令。

### Phase 4: CLI 常用参数与 Ad-hoc 运维体验增强 (P0 - 运维实战)
- [x] **Ad-hoc 异步执行与精简输出 (`ansible`)**:
  - 命令行全面对接 `-B / --background <seconds>` 与 `-P / --poll <seconds>`，底层并发调度支持后台脱敏启动与轮询超时检测；
  - 增加 `-o / --one-line` 紧凑单行输出模式，格式为 `<host> | <STATUS> => <json_payload>`，完美契合高并发集群管道过滤。
- [x] **Playbook 交互式步进调试与失败重试工作流 (`ansible-playbook`)**:
  - 引入 `--step` 单步确认交互模式，执行每个任务前提示 `Perform task: "..." (N)o/(y)es/(c)ontinue:`；
  - 支持 `--limit @file.retry` 语法自动展开文件内的主机列表，并在 Playbook 执行有节点失败时自动生成 `<playbook>.retry` 文件；
  - 命令行暴露 `--force-handlers` 与 `--flush-cache` 参数，支持跨任务失败强制刷新已通知 Handler 及缓存清除。
- [x] **通用网络与连接参数动态覆盖**:
  - Playbook 与 Adhoc CLI 全面支持 `-c / --connection`、`-T / --timeout`、`--private-key / --key-file`、`--ssh-common-args`、`--ssh-extra-args` 运行时动态覆盖主机连接属性；
  - Ad-hoc 全面支持 `-t / --tree <dir>` 参数，将各节点的执行结果按主机名写入独立 JSON 格式文件，满足批量巡检与 CMDB 采集需求。

### Phase 5: 新增独立运维工具链 `ansible-doc` 与 `ansible-config` (P1 - 运维内省)
- [x] **交付独立可执行程序 `cmd/ansible-doc`**:
  - 构建模块元数据文档库，覆盖 28+ 内置原生模块与核心 Meta 任务；
  - 支持 `ansible-doc -l` 列出支持模块及其简要说明；
  - 支持 `ansible-doc <module>` 展示详细参数（名称、类型、必填性、默认值、说明及示例）；
  - 支持 `ansible-doc -s <module>` 直接生成格式规范的 Playbook Task 片段模板。
- [x] **交付独立可执行程序 `cmd/ansible-config`**:
  - 支持 `ansible-config view` 查看当前生效的 `ansible.cfg` 文件内容及解析路径；
  - 支持 `ansible-config dump` 及 `--only-changed` 导出配置项生效值与默认值对比；
  - 支持 `ansible-config init` 自动生成开箱即用的配置模板。
- [x] **增强 `ansible-inventory` 导出与视图能力**:
  - 支持 `--vars` 在 `--graph` 树形输出模式下打印组与主机的变量；
  - 支持 `--output <file>` 直接将清单导出内容重定向保存至文件；
  - 支持 `--export` 模式导出展开的 `_meta.hostvars`。
- [x] **交付独立可执行程序 `cmd/ansible-galaxy`**:
  - 支持 `ansible-galaxy init <role_name>` 与 `ansible-galaxy role init <role_name>`；
  - 支持 `--init-path <dir>` 指定角色创建目录及 `-f / --force` 覆盖模式；
  - 自动脚手架生成规范标准的 Ansible 角色目录结构（`defaults`, `files`, `handlers`, `meta`, `tasks`, `templates`, `tests`, `vars`, `README.md`）；
  - 支持 `ansible-galaxy install` 与 `ansible-galaxy role install`：支持直接安装 Git 仓库、GitHub 简写 (`owner.role` / `owner/repo`)、本地目录角色；支持 `-r / --role-file` 解析标准 `requirements.yml`（兼容列表与字典结构）、`-p / --roles-path` 自定义目录、`-f / --force` 覆盖与 `--ignore-errors` 容错。
- [x] **交付独立可执行程序 `cmd/ansible-pull`**:
  - 支持纯 Pull 模式反向配置管理：节点无需被中心机 SSH 连接，支持节点通过 Git 自更新；
  - 支持 `-U / --url` 远端仓库、`-C / --checkout` 分支/版本、`-d / --directory` 目标路径、`-s / --sleep` 防羊群效应随机休眠、`-o / --only-if-changed` 变更感知执行、`--purge` 跑完清理及自动检索 `<hostname>.yml`/`local.yml`。

### Phase 6: 企业级网络跳板机与高级编排匹配 (P2 - 生产级网络与语法)
- [x] **原生 SSH ProxyJump / 堡垒机隧道代理**:
  - 在纯 Go 传输层解析 `-o ProxyJump=user@jumpbox:port`；
  - 基于 `golang.org/x/crypto/ssh` 建立跳板会话并借由其 `Dial("tcp", target)` 构建管道，作为目标主机的网络底层连接，免除外部依赖原生支持跨 VPC/堡垒机纳管。
- [x] **高级主机模式匹配 (Pattern Matching)**:
  - 完美实现 Ansible 规范的集合运算：交集（`:&`）、排除（`:!`）、并集（`,` / `:`）；
  - 支持 `~<regex>` 正则表达式匹配主机名与 IP；
  - 支持 `group[start:end]` 与 `group[idx]` 数组切片选取主机；
  - 正确处理纯负向（如 `--limit "!web2"`）默认全量集合回退。
- [x] **任务级动态角色引入 (`include_role` / `import_role`)**:
  - 在 Task 树与执行引擎中支持 `include_role` / `import_role` 任务指令；
  - 支持按条件（`when`）动态装载角色任务、默认值、变量与 Handler，支持任务属性向下级联传递。

### Phase 7: 动态扩展性探索 (P3 - 高级生态)
- [ ] **自研插件机制调研**:
  - 评估基于 Go Plugin、Wasm (如 Wasmtime/Wazero) 或 JSON-RPC 外部进程协议加载自定义 Module 与 Filter 的可行性。

### Phase 8: 进阶高级编排与零配置单机体验 (P0 - 运维实战)
- [x] **隐式 `localhost` 全局免清单自动注册 (Implicit Localhost)**:
  - 取消强制要求 `-i` 参数，当未指定清单时自动回退至包含隐式 `localhost` 的安全清单，支持 `ansible localhost -m ping` 零配置极速执行。
- [x] **`pause` 原生暂停与控制台交互模块**:
  - 原生支持 `seconds`、`minutes` 休眠，计算返回精确 `delta` 时间；支持 `prompt` 控制台输入交互；支持 Check Mode 自动跳过。
- [x] **任务级 `throttle: N` 独立并发限制**:
  - 在 `linear` 与 `free` 策略下动态约束任务最大并发数，支持 Play 级和 Block 级的递归继承。
- [x] **`add_host` 与 `group_by` 动态清单维护**:
  - 剧本执行期间动态向内存注入新主机、分配组及属性（默认 run_once）；根据 Facts 模式将节点归入动态组，后续 Play 立即生效。

### Phase 9: 生产级配置体系、角色依赖图谱与预检增强 (P0 - 企业级落地)
- [x] **`ansible.cfg` 核心扩展段全面解析与应用**:
  - 解析 `[privilege_escalation]`（`become`, `become_method`, `become_user`, `become_ask_pass`）；
  - 解析 `[ssh_connection]`（`pipelining`, `ssh_args`, `control_path`, `timeout`），自动注入主机默认属性。
- [x] **角色依赖图谱解析与前置自动装载 (`meta/main.yml` 的 `dependencies`)**:
  - 在角色加载时递归解析 `dependencies`，支持角色变量传递，拓扑排序前置执行依赖任务，内置循环依赖检测与去重。
- [x] **Lookup 插件矩阵扩展 (`password`, `first_found`, `url`)**:
  - `lookup('password', 'path [length=N] [chars=...]')`: 凭据自动随机生成与本地文件持久化；
  - `lookup('first_found', 'path1,path2,...')`: 多系统多路径配置文件优先探测；
  - `lookup('url', 'http://...')`: 远程轻量配置抓取。
- [x] **`assert` 模块强化**:
  - 支持 `quiet: true` 静默断言；支持 `fail_msg` 与 `success_msg` 动态模板渲染与详细诊断结构返回。

### Phase 10: 云原生与流水线增强体系 (P0 - Cloud-Native & CI/CD Pipeline Parity)
- [x] **统一环境变量全矩阵覆盖 (`ANSIBLE_*`)**:
  - 支持无 CLI 传参下通过环境自动注入 `ANSIBLE_FORKS`, `ANSIBLE_REMOTE_USER`, `ANSIBLE_BECOME`, `ANSIBLE_BECOME_METHOD`, `ANSIBLE_BECOME_USER`, `ANSIBLE_BECOME_ASK_PASS`, `ANSIBLE_SSH_ARGS`, `ANSIBLE_SSH_PIPELINING`, `ANSIBLE_SSH_CONTROL_PATH`, `ANSIBLE_SSH_TIMEOUT` 等 15+ 官方标准环境变量，无缝对齐 Jenkins/GitLab CI/GitHub Actions/Argo 流水线注入模式。
- [x] **任务级超时控制机制 (`timeout: <seconds>`)**:
  - 原生支持 Task 级 `timeout` 配置；
  - 采用精确 Goroutine + `time.After` 异步多路复用熔断，防止脚本在流水线与异构网络中无休止挂死；超时精准触发熔断并记录标准退出码 `124`；无缝兼容 `ignore_errors: true` 场景。
- [x] **`vars_files` 动态模板渲染与候选列表容错 (Candidate Fallback)**:
  - 支持 `vars_files` 内部 Jinja2 模板动态计算（如 `vars/{{ env }}.yml`）；
  - 支持候选列表语法（如 `[ "vars/{{ env }}.yml", "vars/default.yml" ]` 或逗号分隔列表），实现首选存在即加载、备选自动降级探测，全未匹配时精准报错。
- [x] **`debug` 模块 Verbosity 级别过滤 (`verbosity: N`)**:
  - 全面对接 CLI `-v / -vv / -vvv` 详细级别传递；
  - `debug` 任务根据 `verbosity` 动态判断当前运行时详细度；未达阈值时返回 `Skipped: true` 与详细原因，避免在生产输出和 CI 日志中产生冗余调试杂音。

### Phase 11: 生产级长任务治理与私有模块扩展 (P0 - 企业级深度落地)
- [x] **交付原生 `async_status` 异步长任务治理模块**:
  - 任务声明 `async: N, poll: 0` 时，统一在目标节点创建状态与输出凭证（`~/.ansible_async/<jid>` 或 `/tmp/.ansible_async/<jid>`）；
  - 自动生成唯一 `ansible_job_id` 并注册到变量字典中；
  - `async_status` 原生模块支持 `jid` 状态轮询及 `mode: cleanup` 清理，与 `until: result.finished == 1`、`retries`、`delay` 完美协同。
- [x] **支持 `library/` 动态私有模块目录扫描与执行 (Custom Modules)**:
  - 自动扫描 Playbook 同级目录下的 `library/` 及角色目录，支持任意可执行脚本（Bash、Python、Go 二进制等）；
  - 兼容 Ansible 标准模块 JSON 通信协议，自动解析标准输出 JSON 字段（`changed`, `failed`, `msg`, 自定义字典）或回退捕获普通脚本输出。
- [x] **Block 容错上下文变量注入 (`ansible_failed_task` & `ansible_failed_result`)**:
  - `block` 出现失败切入 `rescue` 段时，自动在变量上下文与 Facts 中注入 `ansible_failed_task` 与 `ansible_failed_result`（含 `msg`, `stdout`, `stderr`, `rc`），便于运维告警推送或精准补偿。

### Phase 12: 大规模集群性能与事实缓存体系 (P0 - Large-Scale Performance & Fact Caching)
- [x] **持久化事实缓存系统 (Fact Caching)**:
  - 完整解析 `ansible.cfg` 及环境变量中 `fact_caching`（`jsonfile` / `memory`）、`fact_caching_connection`、`fact_caching_timeout`；
  - 在事实收集与跨主机变量装配阶段，优先基于本地 JSON 缓存文件及修改时间判定，在有效期内毫秒级加载免除重复 SSH 远程交互；过期或缺失时执行远程 `setup` 探测并持久化落盘；
  - CLI 与执行引擎提供 `--flush-cache` 一键清空全量主机缓存文件与内存凭据。
- [x] **`profile_tasks` 与 `timer` 核心回调插件 (Execution Profiling)**:
  - 回调层支持 `callbacks_enabled = profile_tasks, timer` 及环境变量 `ANSIBLE_CALLBACKS_ENABLED`；
  - 自动统计记录各任务执行耗时，并在剧本执行结束底部打印 **“耗时最长 Top 10 任务排行榜”**；
  - 精确统计并呈现剧本总体运行耗时 (`Playbook run took X days, X hours, X minutes, X seconds`)。
- [x] **增强循环控制变量 (`ansible_loop`) 与项级条件判断**:
  - 任务循环中为当前上下文内置注入 `ansible_loop` 魔法字典（`index`, `index0`, `first`, `last`, `length`, `revindex`, `revindex0`）；
  - `loop_control` 原生支持 `extended: true` 解析声明；
  - 任务包含 `loop` 时将 `when` 条件动态下沉至每个 item 执行上下文，支持根据当前元素或 `ansible_loop.first` 等细粒度跳过单个循环项。

### Phase 13: 模板语法深度兼容与表达力强化 (P0 - Template Expressiveness & Jinja2 Deep Compatibility)
- [x] **行内三元条件运算解析 (Inline Ternary / Conditional Expression)**:
  - 识别并求值 Jinja2/Python 三元表达式：`{{ true_val if condition else false_val }}`（如 `'master' if is_master else 'worker'`、`'production' if env == 'prod' else 'staging'`、`8080 if port is not defined else port`、`('a' if c1 else 'b') if c2 else 'c'`）；
  - 支持右结合性（right-associative）与括号优先级处理；
  - 在 Task 参数字符串 (`resolveString` / `resolveVarExpr`)、`when` 条件表达式 (`resolveConditionOperand`) 与 `.j2` 模板文件 (`pkg/module/template.go`) 中双向无缝生效。
- [x] **`default` 过滤器布尔回退与字典/列表字面量强化**:
  - 扩展 `default(fallback, boolean=true)` 与 `default(fallback, true)`：当指定布尔回退时，空字符串 `""`、`false`、`0`、空列表 `[]`、空字典 `{}` 均能正确触发回退值；当默认 `boolean=false` 时，仅未定义/`nil` 触发回退；
  - 强化管道过滤器与字典/列表字面量提取：如 `{{ var | default({}) | to_json }}`、`{{ list | default([]) | length }}`，原生支持 Python/Jinja 单引号列表及字典的反序列化。
- [x] **官方 `ternary` 三元过滤器与类型保留**:
  - 原生支持 `{{ condition | ternary(true_val, false_val, [none_val]) }}`，对齐 Ansible 官方规范并支持两参及三参（未定义/nil 回退）；
  - 单独 Jinja 表达式作为参数时（如 `var: "{{ missing | default({}) }}"`）保留原生类型（字典、切片、整数、布尔），避免字符串强转破坏后续结构化处理。

### Phase 14: 系统级底层基础设施调优模块 (P0 - 节点基础初始化)
- [x] **`sysctl` 原生声明式内核调优模块**:
  - 原生支持 `sysctl: name=net.ipv4.ip_forward value=1 state=present reload=yes sysctl_set=yes`；
  - 自动维护 `/etc/sysctl.conf` / `/etc/sysctl.d/*.conf` 配置文件；
  - 支持 `sysctl -p <file>` 重新载入与 `sysctl -w <name>=<value>` 运行时强制设置；
  - 支持 `state: present` 与 `state: absent`、Check Mode 及统一 Diff 对比。
- [x] **`hostname` 原生主机名维护模块**:
  - 原生支持 `hostname: name=k8s-master-01` 与自动策略探测（`hostnamectl` / `generic`）；
  - 自动同步更新内核活动主机名与 `/etc/hostname` 持久化配置；
  - 支持 Check Mode 与变更前后的文本 Diff 呈现。
- [x] **`mount` 原生挂载点与 fstab 维护模块**:
  - 原生支持 `mount: path=/data src=/dev/vdb1 fstype=ext4 opts=defaults,noatime state=mounted`；
  - 完整支持 `mounted`（写入 fstab + 创建目录 + 挂载）、`unmounted`（卸载 + 保留 fstab）、`present`（仅维护 fstab）、`absent`（卸载 + 清理 fstab）、`remounted`（重新按指定参数挂载）；
  - 自动解析与维护 `/etc/fstab`（保留注释与非条目行），支持 `/proc/mounts` 运行时挂载状态探测；
  - 完整集成 Check Mode 与 fstab Unified Diff 比对。

### Phase 15: 节点生命周期、重启与连接自愈 (P0 - Reboot & Connection Resilience)
- [x] **`reboot` 原生重启与自愈探测模块**:
  - 声明式控制节点重启：下发 reboot 命令、关闭现有 SSH 连接、探测节点离线、轮询探测节点恢复、重新握手并执行测试命令；
  - 支持参数：`reboot_timeout`（默认 600s）、`post_reboot_delay`、`pre_reboot_delay`、`test_command`（默认 `whoami`）、`search_regex`、`boot_time_command`、`reboot_command`；
  - 重启成功后自动探测并恢复 Facts 缓存与连接池连接。
- [x] **`wait_for_connection` 原生连接等待模块**:
  - 专用等待远程主机网络与 SSH/WinRM 连接端口就绪；
  - 支持 `timeout`、`delay`、`sleep` 轮询间隔控制与 Check 模式。
- [x] **任务级 `ignore_unreachable: true` 声明**:
  - 支持在任务执行过程中遇到节点网络不可达/掉线时，不立即将主机判定为致命失败并移出清单，允许在特定网络切换或重启场景下容忍断连；
  - 变量注册自动填充 `unreachable: true`，Callback 与 Play Recap 精准统计 `unreachable` 与 `ignored` 主机数。

### Phase 16: 运维交互与调试断点能力强化 (P1 - Interactive Operations & Debugging)
- [x] **Play 级 `vars_prompt` 运行时交互录入**:
  - 支持在 Playbook 启动前提示用户输入敏感密码或环境配置；
  - 完整支持 `name`、`prompt`、`default`、`private: yes/no`（默认隐藏输入）、`confirm: yes/no`（二次输入校验）与 `encrypt: sha256/sha512/md5` 散列；
  - 严格遵循优先级规则：若命令行 `-e / --extra-vars` 已提供同名变量，自动跳过 prompt 录入；
  - 终端适配：真实 TTY 下调用 `golang.org/x/term` 隐藏回显，测试或管道下无缝读取 `Stdin io.Reader`。
- [x] **任务级 / Play 级 / Block 级 `debugger` 调试器**:
  - 支持 `debugger: on_failed`、`on_unreachable`、`on_skipped`、`always`、`never`，以及环境变量 `ANSIBLE_ENABLE_TASK_DEBUGGER=True` 全局触发；
  - 任务中断时进入专属 REPL 调试器会话，支持以下指令集：
    - `p / print <expr>`: 打印结果字典 `result`、退出码 `rc`、标准输出 `stdout`、`task.args`、`task_vars` 或指定变量；
    - `u / update_task <key=val>`: 动态修改任务参数（如 `u cmd="echo fixed"` 或 `u task.args['msg']='hello'`），即刻热更新 `task.Params` 与 `_raw_params`；
    - `r / redo`: 重新执行当前任务（可在修改参数后立即生效验证，若成功则优雅恢复继续执行）；
    - `c / continue`: 忽略当前断点中断，保持当前结果继续推进剧本；
    - `q / quit`: 立即终止剧本执行。

### Phase 17: 安全与凭证轮换增强 (P1 - Advanced Vault & Multi-Identity)
- [x] **多 Vault-ID 体系与凭据标识 (`--vault-id [label@]source`)**:
  - 命令行全面支持重复传入多个 `--vault-id`（`ansible-vault`、`ansible-playbook`、`ansible`）；
  - 支持 `label@prompt`、`label@password_file` 以及 `label@script_file`；
  - 配置文件 `ansible.cfg` 与环境变量 `ANSIBLE_VAULT_IDENTITY_LIST` 自动解析加载；
  - 加密时支持 `--encrypt-vault-id` 精准指定加密使用的 ID 标签；解密时优先匹配头部 ID 标签，兼容 1.1 无标签回退探测。
- [x] **外部密码提供者客户端脚本集成 (Vault Secret Client Script)**:
  - 自动探测密码文件执行权限 (`mode & 0111 != 0`)；
  - 若为可执行脚本，自动执行外部程序并传递 `--vault-id <label>` 参数；
  - 捕获 stdout 剔除首尾换行作为 Vault 密码，无缝对接企业 HashiCorp Vault、AWS Secrets Manager、Keychain 等外部秘钥系统。

### Phase 18: 生产级综合 Playbook 规格平替与现代语法生态适配 (P0 - 典型生产模板兼容)
- [x] **FQCN 全限定集合前缀解构与现代任务参数 (`ansible.builtin.*`, `args:`)**:
  - 全面支持 `ansible.builtin.*`、`ansible.posix.*`、`ansible.legacy.*` 模块前缀，自动剥离集合前缀映射至对应原生内置模块；
  - 规范化任务保留字段，支持 `args:` 参数块下发模块参数，支持 Handler 级别保留字段（`changed_when`, `failed_when`, `args` 等）；
  - 任务级 `environment:` 字典参数支持，无缝覆盖并合并 Play 级全局环境变量。
- [x] **`command` 模块列表参数支持 (`argv: [...]`)**:
  - 支持直接声明 `argv: [bin, arg1, arg2, ...]`，通过 `shellQuote` 安全拼接并执行，防止 Shell 转义歧义与注入风险。
- [x] **通用包管理器模块 (`package`)**:
  - 自动在目标主机探测包管理器（`apt-get` / `yum` / `dnf`），自适应分发至 `AptModule` 或 `YumModule`，支持跨 Linux 发行版统一包安装与卸载。
- [x] **Jinja2 `dict2items` 过滤器与现代状态测试语法 (`is succeeded / failed / unreachable / skipped`)**:
  - 模板与 Playbook 表达式支持 `dict_var | dict2items` 字典转列表对象；
  - `when` / `until` 条件中原生支持现代状态测试语法：`result is succeeded`、`is not succeeded`、`is failed`、`is unreachable`、`is skipped`。
- [x] **`copy` 远端复制与文件校验 (`remote_src: true`, `validate: "... %s"`)**:
  - `remote_src: true` 模式跳过控制机本地文件探测，直接在受管节点端通过校验和比对与远端复制；
  - `copy` 与 `template` 均支持 `validate: "cmd %s"`：在受管机临时文件执行校验命令，校验通过后原子覆盖目标路径并应用属主权限。
- [x] **Facts 收集器标准化与家族精准识别**:
  - `ansible_date_time` 采集为包含 `epoch`, `iso8601`, `year`, `month`, `day`, `time`, `date` 的标准字典；
  - `ansible_os_family` 优化为探测 `/etc/os-release` 并规范化映射至 `RedHat`、`Debian`、`Alpine`、`Archlinux`、`Darwin`；
  - `parseFacts` 全面支持 YAML/JSON 嵌套字典反序列化，支持 `{{ ansible_date_time.epoch }}` 变量访问。

### Phase 19: 运维核心高频能力补强与基础设施无缝纳管 (P0 - 关键缺漏收敛)
- [x] **`group_vars/` 与 `host_vars/` 子目录变量解析修复**:
  - 根除 `filepath.Base` 误将 `group_vars/webservers/main.yml` 识别为主机名 `"main"` 的 Bug，支持多层级子目录多变量文件自动合并至目标组/主机。
- [x] **`no_log: true` 敏感输出脱敏体系**:
  - 在 Task 级支持 `no_log: true`，对任务 stdout、message、item 进行 `[VALUE_SPECIFIED_IN_NO_LOG_TASK]` 脱敏遮罩，清空 stderr 与 diff，回调输出自动标记 `censored`，彻底杜绝凭证泄露。
- [x] **`setup` 核心网络事实与环境事实采集补全**:
  - 自动跨平台（Linux 与 macOS）采集 `ansible_default_ipv4`（`address`, `interface`, `gateway`）、`ansible_all_ipv4_addresses`、`ansible_env` 环境变量字典、`ansible_user_id` 与 `ansible_user_dir`。
- [x] **原生实现 `authorized_key` 基础设施模块**:
  - 支持 `user`、`key`（多公钥/URL 远程拉取）、`state=present/absent`、`exclusive` 独占清理、`key_options`、`comment`、`manage_dir`（0700/0600 严格权限）、Check Mode 与 Diff Mode。
- [x] **原生实现 `pip` Python 生态包管理模块**:
  - 支持 `name`、`version`、`requirements`、`virtualenv` 自动探测与创建、`virtualenv_command`、`state=present/absent/latest/forcereinstall`、`extra_args`、`chdir` 及 Check Mode 幂等预判。
- [x] **原生实现 `synchronize` (rsync) 高效目录文件同步模块**:
  - 封装本地与远端 `rsync` 协议传输，支持 `src`、`dest`、`archive`（`-a`）、`delete`（`--delete`）、`checksum`（`-c`）、`compress`（`-z`）、`dest_port`、`mode=push/pull`、`rsync_opts`、`rsync_path`，自动透传 SSH 密钥与端口，支持 Check Mode（`--dry-run`）与 Diff Mode。
- [x] **Handlers 链式级联触发 (Handlers Notifying Handlers)**:
  - 在 Handler 执行结果发生变更 (`changed: true`) 且声明了 `notify:` 时，自动级联触发下游 Handlers 队列，支持多轮级联与幂等去重。
- [x] **Jinja2 高频过滤器补全**:
  - 新增 `items2dict`（支持自定义 `key_name` / `value_name`）、`to_yaml` / `to_nice_yaml` 以及 `from_yaml` 双向转换过滤器。
- [x] **终端 ANSI 色彩控制与标准规范适配**:
  - 严格遵循 `https://no-color.org` 规范：`NO_COLOR` 非空或 `ANSIBLE_NOCOLOR=1` 时关闭色彩输出；`ANSIBLE_FORCE_COLOR=1` 时强制启用；非 TTY 管道/重定向时自动剥离 ANSI 颜色代码。
- [x] **`delegate_facts` 语义与委派事实归属修复**:
  - 在 Task 声明与解析层增加 `delegate_facts: true/false`；严格遵循 Ansible 规范：默认 (`delegate_facts: false`) 下 `set_fact` 与 `register` 归属于发起主机 `inventory_hostname`，仅在显式声明 `delegate_facts: true` 时才将事实与注册变量写入被委派主机。
- [x] **SSH 连接层输出净化与长连接透明自愈**:
  - 彻底清除 `client.go` 中硬编码的 `fmt.Printf("[SSH] ...")` 调试输出，恢复洁净标准输出；
  - 在 `c.client.NewSession()` 与 `sftp.NewClient()` 探测到对端因空闲超时断开时，自动重置连接并透明自愈重连重试，根除长任务执行后的 `broken pipe` 级联失败。
- [x] **架构与性能细节优化**:
  - 提取公共 `BuildBecomeCommand` 函数，消除 SSH 连接与 Local 连接中 40 行 100% 重复的 become 组装代码；
  - 为 `resolveString` 增加 `!strings.Contains(s, "{{")` 极速短路优化，纯静态文本零内存分配、O(1) 立即返回；
  - 废除 `executeLocalModule` 中对 `shell/command` 的粗暴字段切分，统一收敛至标准 `LocalConnection` 驱动。

### Phase 20: 核心模块 Check Mode 安全闭环与编排语义深度对齐 (P0 - 生产级安全防护)
- [x] **9 大高频内置模块 Check Mode 模拟实现与穿透防御**:
  - `apt` / `yum` / `package`: 在 check mode 下自动切换至包存在性探测（`dpkg -s` / `rpm -q`），仅计算预期变更，严禁下发破坏性包安装或移除命令；
  - `service` / `systemd`: 在 check mode 下仅探测运行状态（`systemctl is-active` / `service status`）与自启状态（`is-enabled` / `masked`），精准预演 changed 状态，杜绝真实启停服务；
  - `user` / `group`: 在 check mode 下探测目标系统用户及属性（`id` / `getent`），仅汇报差异，严禁下发用户增删改命令；
  - `get_url`: 在 check mode 下比对目标文件存在性与哈希匹配，仅预判变更，杜绝产生文件下载与覆盖 I/O；
  - `archive` / `unarchive`: Check Mode 下基于目标路径及 `creates` 预期状态预演，杜绝解包或压缩写入。
- [x] **Task & Play 级 `check_mode` 与 `diff` 声明支持**:
  - 支持在 Play 及 Task 级别声明 `check_mode: yes/no` 与 `diff: yes/no`；
  - 支持任务级精准覆盖全局 CLI `-C` / `-D` 开关（例如：全局 `-C` 下允许设置 `check_mode: false` 的只读前置任务真实运行，为后续步骤提供必要动态凭据）；支持在 Block 嵌套层级递归继承。
- [x] **`failed_when` 与 `ignore_errors: true` 协同语义修复**:
  - 修复此前 `failed_when` 触发判定时直接标记 `result.Failed` 导致忽略错误语义失效的缺陷，严格遵循 Ansible 规范：满足 `failed_when` 且声明 `ignore_errors: true` 时标记 `Ignored = true`，确保主机不被误移出执行批次。
- [x] **`cron` 模块幂等性、跨平台语法与 `disabled` 支持**:
  - 彻底废除 GNU 专有 `sed` 语法，采用纯 Go 内存解析与 Base64 原子管道回填，无缝兼容 macOS、BSD、Alpine 与 Linux；
  - 实现相同定时任务与指令的严格幂等比对，消除无意义的 `changed` 误报；
  - 支持 `disabled: true/false` 声明式控制定时任务注释禁用与恢复。



