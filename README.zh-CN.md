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

**2. 在分享者的机器上安装 CLI 和 Herdr mod**（macOS / Linux；Windows 从 [Releases](https://github.com/jtsang4/herdr-share/releases) 下载 zip）：

```sh
curl -fsSL https://raw.githubusercontent.com/jtsang4/herdr-share/main/scripts/install.sh | sh
```

这会把 `herdr-share` 装到 `~/.local/bin`，在 Herdr 里加入分享对话框，并绑定 **⌘⇧S**（Linux/Windows 为 Ctrl+Shift+S）来打开它。设置 `HERDR_SHARE_NO_BIND=1` 则不改动你的 `keys.json`。

**3. 分享**：在 Herdr 里聚焦要分享的终端，按 **⌘⇧S**。第一次会让你填中继地址（和 token）；之后选择“谁能操作”和“链接有效期”，点 **Start sharing**，链接自动复制，发给对方即可。

命令行同样可以：

```sh
herdr-share config set relay https://share.example.com
herdr-share config set token <token>
herdr-share start t7 --mode view --ttl 2h --copy   # t7 是终端地址（见 `hc ls`）
herdr-share mode t7 control                        # 允许对方操作
herdr-share stop t7                                # 链接立即失效
```

分享者和被分享者的完整使用流程见 [docs/usage.md](docs/usage.md)。

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
