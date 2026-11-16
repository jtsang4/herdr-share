# herdr-share

把 [Herdr](https://herdr.dev) 里正在运行的终端会话（工作中的 agent、shell）实时分享给别人，经由你自己部署的中继服务。对方用浏览器打开链接就能看。能不能操作在分享时决定，分享过程中随时可以改；停止分享后对方立刻断开，链接永久失效，你的会话照常运行。

[English](README.md)

- **一个链接，不用注册**：对方只需要浏览器。
- **你决定谁能操作**：默认只读，随时切到“可操作”再切回来。
- **中继自己部署**：一个很小的容器（`ghcr.io/jtsang4/herdr-share-relay`），内网、隔离网络都能用；中继地址是可配置项。
- **端到端加密**：密钥只在链接的 `#` 片段里，浏览器从不把它发给服务器。中继只转发它读不了、改不了、也无法重放的密文。
- **不改变你的终端尺寸**：对方看到的是你的终端尺寸，对方打字也不会改变它。

## 快速开始

**1. 部署中继**（一次即可，个人或团队共用），详见 [docs/deploy.md](docs/deploy.md)：

```sh
docker run -d --name herdr-share-relay -p 8080:8080 \
  -e HERDR_SHARE_TOKENS=$(openssl rand -hex 16) \
  ghcr.io/jtsang4/herdr-share-relay:latest
```

公网使用请放在 HTTPS 后面（仓库里有带 Caddy 的 [compose 文件](deploy/docker-compose.yml)）。

**2. 在分享者的机器上把分享对话框装进 Herdr**（macOS / Linux / Windows，需要 Node.js 22 及以上）：

```sh
npx -y herdr-share@latest mod install
```

不会全局安装任何东西：npx 拉取 npm 包，由它从 release 下载对应平台的原生二进制并校验 SHA-256，二进制就放在包里（npx 的缓存中）。这一步在 Herdr 里加入分享对话框，并绑定 **⌘⇧S**（Linux/Windows 为 Ctrl+Shift+S）打开它，不改你的布局；加 `--no-bind` 则不改动 `keys.json`。

![Herdr 里的分享对话框](docs/images/share-dialog.png)

**3. 分享**：在 Herdr 里聚焦要分享的终端，按 **⌘⇧S**。第一次会让你填中继地址（和 token）；之后选择“谁能操作”和“链接有效期”，点 **Start sharing**，链接自动复制，发给对方即可。

命令行同样可以（下面的 `herdr-share` 指 `npx --prefer-offline -y herdr-share`；全局安装过则直接用命令）：

```sh
herdr-share config set relay https://share.example.com
herdr-share config set token <token>
herdr-share start t7 --mode view --ttl 2h --copy   # t7 是终端地址（见 `hc ls`）
herdr-share mode t7 control                        # 允许对方操作
herdr-share stop t7                                # 链接立即失效
```

**也可以直接让 coding agent 来做**：装一次 skill，然后说“把当前会话只读分享给 Alice，30 分钟”即可：

```sh
npx skills add jtsang4/herdr-share --skill herdr-share
```

分享者和被分享者的完整使用流程见 [docs/usage.md](docs/usage.md)。

## 安装、更新与卸载

| | npx（推荐） | npm 全局安装 | 安装脚本 / 手动 |
|---|---|---|---|
| 安装 | `npx -y herdr-share@latest mod install` | `npm i -g herdr-share && herdr-share mod install` | `curl -fsSL https://raw.githubusercontent.com/jtsang4/herdr-share/main/scripts/install.sh \| sh` |
| 日常使用 | `npx --prefer-offline -y herdr-share …` | `herdr-share …` | `herdr-share …` |
| 更新 | `npx -y herdr-share@latest version` | `npm i -g herdr-share@latest` | 重新运行安装脚本 |
| 卸载 | `npx -y herdr-share uninstall` | `herdr-share uninstall && npm uninstall -g herdr-share` | `herdr-share uninstall`，再删掉 `~/.local/bin/herdr-share` |

- **默认就是 npx 模式**：不往 PATH 里放任何东西，Herdr 里的 mod 通过 `npx -y herdr-share@<版本>` 调用（走 npx 缓存）。如果选择全局安装，`herdr-share` 命令放在 npm 正常的全局 bin 目录，二进制随包放在全局 `node_modules` 里。
- **不会反复下载，也不会每次查版本**：同一版本的二进制只下载一次；`--prefer-offline` 让 npx 直接用缓存、不去问 registry，首次之后每条命令都能离线运行，体验和全局命令一样。只有带 `@latest` 时才检查新版本。
- **更新**（需要时执行一次 `@latest`）只下载一次新版本，并自动刷新 Herdr 里的 mod；正在进行的分享继续用旧二进制直到结束。
- **`herdr-share uninstall`** 会停止所有分享、移除 Herdr mod 和快捷键、删除配置与状态（加 `--keep-config` 保留配置）。

## 安全

“可操作”模式下，拿到链接的人就能以你的身份在这个终端里执行命令（agent 也能执行 shell 命令）。这类链接只发给信任的人，有效期尽量短，并用 token 保护你的中继。详见 [docs/security.md](docs/security.md)。

## 开发

```sh
make web build      # 先构建访客网页，再构建 ./bin/herdr-share 和 ./bin/herdr-share-relay
make test lint      # 单元测试与静态检查
make e2e            # 隔离环境端到端：独立 herdr-core 会话、中继、CLI、真实浏览器
```

## 许可证

[MIT](LICENSE)
