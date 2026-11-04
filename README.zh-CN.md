# LRAP-Portal
[English](README.md) | 中文

路由器自带的门户（路由器自带网页）：用户在路由器本机上打开的页面，以及页面背后的后端 `lrapServer`。两者都随固件出厂。设备网络的本地配置都在这里完成（接口、DHCP、Wi-Fi、EasyMesh、客户 VLAN、天线模块），云端能要求执行的少数几项变更也由它来落实。

## 在系统中的位置

一台设备在工厂完成生产（lrap-anchor、lrap-keel、lrap-harbor），之后由云平台（lrap-beacon）通过设备上的 Agent（lrap-buoy）管理。Agent 自己没有配置写入路径：重启、安装固件（始终保留设置）和修改两个 Wi-Fi 网络名称，都通过 `http://127.0.0.1:9080` 交给本门户执行，而且 Agent 只在装有门户的设备上声明这几项能力（capability）（它检查 `/etc/config/lrapServer` 是否存在）。客户 VLAN 在这里配置；云端控制台只做展示。一台设备的完整流程见 lrap-ops 的 [`docs/LIFECYCLE.zh-CN.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/LIFECYCLE.zh-CN.md)；固件必须包含的内容（包括本门户）见 lrap-ops 的 [`docs/FIRMWARE.zh-CN.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/FIRMWARE.zh-CN.md)。

在路由器上：

- 页面是 `/www` 下的静态文件，由路由器的 uhttpd 提供（80 或 443 端口）。
- `lrapServer` 是一个 procd 服务（`/etc/init.d/lrapServer`），监听 `:9080`。它响应页面发来的 `/api/...` 请求，通过路由器的 ubus（`http://127.0.0.1/ubus`）完成操作。页面总是调用“打开它的那台主机”的 9080 端口（`src/utils/http.ts`），因此在 mesh 组网中，每台 AC 的门户都只和自己的后端通信。
- 账号：`admin` 在门户中拥有全部权限（恢复出厂后密码回到 `admin`）；root 使用每台设备各自的密码，由 Harbor 保管。见 lrap-ops 的 [`docs/OPENWRT-HARDWARE.zh-CN.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/OPENWRT-HARDWARE.zh-CN.md)。

## 代码结构

| 路径 | 内容 |
|---|---|
| `src/` | 前端：React 19、TypeScript、Vite 6、Tailwind CSS 4、shadcn/ui 组件（`components.json`、`src/components/ui/`）、zustand 状态（`src/states/`） |
| `src/Pages/` | 每个菜单一个目录：`Statuspages`、`Networkpages`、`MTKpages`（Wi-Fi、EasyMesh）、`Systempages`、`Servicespages`、`Loginpages` |
| `src/utils/http.ts` | 页面如何找到 API（`VITE_API_BASE`，或者当前主机的 9080 端口） |
| `public/`、`index.html` | 静态文件，以及 Vite 构建的入口页面 |
| `backend/` | `lrapServer`，Go 后端（模块名 `lrap`，只用标准库），以及它的测试 |
| `backend/etc/` | 它的 procd 服务（`init.d/lrapServer`）、它的配置（`config/lrapServer`）、`rpcd` 和 `uhttpd` 的配置，以及首次启动时创建 `admin` 账号的 `uci-defaults/99-lrap-accounts` |
| `backend/usr/share/rpcd/acl.d/` | 门户的 rpcd 访问规则（4 个文件） |
| `backend/subModule/unauthenticated.json` | 给天线子模块用的 rpcd 访问规则文件 |
| `mtk_sta_info.go` | 从 MediaTek 驱动读取 Wi-Fi 客户端信息的 Go 小程序；安装为 `/usr/bin/mtk_sta_info` |
| `mtk_mactable_probe.go` | 探测 MediaTek 驱动的终端表（`iwpriv ra0 get_mac_table`） |
| `mt7981.dbdc.b0.dat`、`mt7981.dbdc.b1.dat` | MediaTek MT7981 Wi-Fi 驱动的配置文件 |
| `cgi-bin/` | 早期的 CGI shell 脚本；现在的页面和 `lrapServer` 都不调用它们 |
| `default/default.html` | 跳转到 LuCI（`cgi-bin/luci/`）的页面 |

以下内容不在 git 中（`.gitignore`），但本地检出目录里可能有：`node_modules/`、`dist/`、编译出来的程序（`lrapServer`、`backend/lrapServer`、`mtk_sta_info`、`mtk_mactable_probe`、`*.exe`）、固件镜像和交付包（`firmware/`、`fw-*/`、`LRAPAssets*/`、`LRAPAssests*/`、`*.zip`、`*.bin`），以及 `site.tar`。

## 前置条件

- Node.js 和 npm。Vite 6.3 要求 Node 18、20，或 22 及以上。
- Go 1.25（`backend/go.mod` 中为 `go 1.25.1`）。后端只用标准库，因此不需要设置 `GOPRIVATE`。

路由器是 OpenWrt MT7981 主模块，ARM64 架构。Go 程序以 `CGO_ENABLED=0` 为 `linux/arm64` 构建，交叉编译不需要 C 工具链，也不需要 OpenWrt SDK。

## 前端

```sh
npm ci
npm run lint     # ESLint
npm run build    # tsc -b && vite build: writes dist/
npm run dev      # Vite dev server on port 5173
```

`dist/` 里是 `index.html`、`favicon.svg`、`vite.svg` 和 `assets/`：这些就是放进固件 `/www` 的内容。

用 `npm run dev` 时，页面由你的电脑提供，API 却在路由器上。`VITE_API_BASE` 可以覆盖页面发送 API 请求的地址（`src/utils/http.ts`），另外 `vite.config.ts` 还把 `/api` 代理到 `http://10.10.18.1:9080`。`lrapServer` 只接受 `/etc/config/lrapServer` 中 `cors_origin`（`CORS_ORIGIN`）所指定来源的跨域页面请求；默认值 `*` 表示只接受路由器自己的页面。修改开发用路由器的配置，必须先得到项目负责人的授权（lrap-ops 的 `AGENTS.md`）。

## 后端

在 `backend/` 中为路由器构建 `lrapServer`：

```sh
cd backend
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o lrapServer .
```

```powershell
cd backend
$env:GOOS='linux'; $env:GOARCH='arm64'; $env:CGO_ENABLED='0'
go build -trimpath -o lrapServer .
```

`mtk_sta_info` 用同样的方法在仓库根目录构建：设置同样的三个变量，执行 `go build -trimpath -o mtk_sta_info mtk_sta_info.go`。

测试（在开发电脑上运行，不需要路由器；约 45 秒）：

```sh
cd backend
go vet .
go test . -count=1
```

`lrapServer` 从环境变量读取设置，这些变量由 `/etc/init.d/lrapServer` 根据 `/etc/config/lrapServer` 填入：`LISTEN_ADDR`（`:9080`）、`UBUS_URL`（`http://127.0.0.1/ubus`）、`CORS_ORIGIN`（`*`），以及 `TLS_CERT_FILE` 和 `TLS_KEY_FILE`（都为空时使用普通 HTTP；两者都设置时提供 HTTPS）。

本仓库没有 CI workflow（lrap-ops 的 [`docs/CI.zh-CN.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/CI.zh-CN.md)）：提交 pull request 之前请自己跑 lint、构建和后端测试。

## 如何进入固件

门户没有版本 tag：固件取的是当前 `main` 分支，交给固件工程师的文件要附上各自的 SHA-256。每个文件放在哪里（lrap-ops 的 [`docs/FIRMWARE.zh-CN.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/FIRMWARE.zh-CN.md) 中的“主模块”）：

| 本仓库中的来源 | 在固件中的位置 | 权限 |
|---|---|---|
| `dist/index.html`、`favicon.svg`、`vite.svg`、`dist/assets/*` | `/www/index.html`、`favicon.svg`、`vite.svg`、`/www/assets/*`。`assets/` 要整体替换，不留旧文件；保留 `cgi-bin` 和 `luci-static` | 644 |
| `lrapServer`（按上文构建）、`backend/etc/init.d/lrapServer` | `/usr/sbin/lrapServer/lrapServer`、`/etc/init.d/lrapServer` | 755 |
| `mtk_sta_info`（按上文构建） | `/usr/bin/mtk_sta_info`，主模块和天线子模块上都有 | 755 |
| `backend/etc/uci-defaults/99-lrap-accounts` | `/etc/uci-defaults/99-lrap-accounts` | 755 |
| `backend/etc/config/lrapServer`、`rpcd`、`uhttpd` | `/etc/config/` | 644 |
| `backend/usr/share/rpcd/acl.d/*.json`（4 个文件） | `/usr/share/rpcd/acl.d/` | 644 |

之后，固件版本只通过云平台的发布存储桶、由客户自己发起的分批升级（rollout）到达客户手中（lrap-ops 的 `Publish-Firmware.ps1`；[`docs/CLOUD.zh-CN.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/CLOUD.zh-CN.md) 中的“固件版本（S3）”）。

## Vite 模板说明

前端最初是从 Vite 的 React + TypeScript 模板起步的。以下是模板自带的说明：

这个模板提供了一个最小化的配置，让 React 在 Vite 中运行，支持 HMR，并带有一些 ESLint 规则。

目前有两个官方插件可用：

- [@vitejs/plugin-react](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react) 使用 [Babel](https://babeljs.io/) 实现 Fast Refresh
- [@vitejs/plugin-react-swc](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react-swc) 使用 [SWC](https://swc.rs/) 实现 Fast Refresh

### 扩展 ESLint 配置

如果是开发正式上线的应用，建议更新配置，启用带类型检查的 lint 规则：

```js
export default tseslint.config({
  extends: [
    // Remove ...tseslint.configs.recommended and replace with this
    ...tseslint.configs.recommendedTypeChecked,
    // Alternatively, use this for stricter rules
    ...tseslint.configs.strictTypeChecked,
    // Optionally, add this for stylistic rules
    ...tseslint.configs.stylisticTypeChecked
  ],
  languageOptions: {
    // other options...
    parserOptions: {
      project: ['./tsconfig.node.json', './tsconfig.app.json'],
      tsconfigRootDir: import.meta.dirname
    }
  }
})
```

还可以安装 [eslint-plugin-react-x](https://github.com/Rel1cx/eslint-react/tree/main/packages/plugins/eslint-plugin-react-x) 和 [eslint-plugin-react-dom](https://github.com/Rel1cx/eslint-react/tree/main/packages/plugins/eslint-plugin-react-dom)，获得针对 React 的 lint 规则：

```js
// eslint.config.js
import reactX from 'eslint-plugin-react-x'
import reactDom from 'eslint-plugin-react-dom'

export default tseslint.config({
  plugins: {
    // Add the react-x and react-dom plugins
    'react-x': reactX,
    'react-dom': reactDom
  },
  rules: {
    // other rules...
    // Enable its recommended typescript rules
    ...reactX.configs['recommended-typescript'].rules,
    ...reactDom.configs.recommended.rules
  }
})
```
