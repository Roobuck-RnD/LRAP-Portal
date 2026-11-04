# LRAP-Portal
English | [中文](README.zh-CN.md)

The router's own web portal: the pages a person opens on the router itself, and the backend behind them, `lrapServer`. Both ship in the firmware. It is where a unit's network is configured locally (interfaces, DHCP, Wi-Fi, EasyMesh, customer VLANs, the antenna modules), and it is what carries out the few changes the cloud may ask for.

## Where it fits

A unit is made in the factory (lrap-anchor, lrap-keel, lrap-harbor) and then managed from the cloud platform (lrap-beacon) through its agent (lrap-buoy). The agent has no configuration write path of its own: rebooting, installing firmware (always keeping settings) and changing the two Wi-Fi network names are handed to this portal, on `http://127.0.0.1:9080`, and the agent claims those capabilities only where the portal is installed (it looks for `/etc/config/lrapServer`). Customer VLANs are configured here; the cloud console only shows them. The whole path of a unit is in lrap-ops [`docs/LIFECYCLE.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/LIFECYCLE.md); what the firmware must carry, this portal included, is in lrap-ops [`docs/FIRMWARE.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/FIRMWARE.md).

On the router:

- The pages are static files in `/www`, served by the router's uhttpd (port 80 or 443).
- `lrapServer` is a procd service (`/etc/init.d/lrapServer`) listening on `:9080`. It answers the pages' `/api/...` calls and works through the router's ubus (`http://127.0.0.1/ubus`). The pages call port 9080 of whichever host they were opened from (`src/utils/http.ts`), so in a mesh each AC's portal talks to its own backend.
- Accounts: `admin` may do everything in the portal (its password goes back to `admin` on a factory reset); root has the unit's own password, held by Harbor. See lrap-ops [`docs/OPENWRT-HARDWARE.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/OPENWRT-HARDWARE.md).

## Code layout

| Path | Contents |
|---|---|
| `src/` | The front end: React 19, TypeScript, Vite 6, Tailwind CSS 4, shadcn/ui components (`components.json`, `src/components/ui/`), zustand stores (`src/states/`) |
| `src/Pages/` | One folder per menu: `Statuspages`, `Networkpages`, `MTKpages` (Wi-Fi, EasyMesh), `Systempages`, `Servicespages`, `Loginpages` |
| `src/utils/http.ts` | Where the pages find the API (`VITE_API_BASE`, or port 9080 of the current host) |
| `public/`, `index.html` | Static files and the page Vite builds from |
| `backend/` | `lrapServer`, the Go backend (module `lrap`, standard library only), with its tests |
| `backend/etc/` | Its procd service (`init.d/lrapServer`), its configuration (`config/lrapServer`) and the `rpcd` and `uhttpd` configuration, and `uci-defaults/99-lrap-accounts`, which creates the `admin` account on first boot |
| `backend/usr/share/rpcd/acl.d/` | The portal's rpcd access rules (4 files) |
| `backend/subModule/unauthenticated.json` | An rpcd access file for the antenna submodules |
| `mtk_sta_info.go` | Small Go program reading Wi-Fi client information from the MediaTek driver; installed as `/usr/bin/mtk_sta_info` |
| `mtk_mactable_probe.go` | A probe of the MediaTek driver's station table (`iwpriv ra0 get_mac_table`) |
| `mt7981.dbdc.b0.dat`, `mt7981.dbdc.b1.dat` | MediaTek MT7981 Wi-Fi driver profiles |
| `cgi-bin/` | Older CGI shell scripts; neither the current pages nor `lrapServer` call them |
| `default/default.html` | A page that redirects to LuCI (`cgi-bin/luci/`) |

Not in git (`.gitignore`), though they may be in a local checkout: `node_modules/`, `dist/`, the compiled binaries (`lrapServer`, `backend/lrapServer`, `mtk_sta_info`, `mtk_mactable_probe`, `*.exe`), firmware images and delivery bundles (`firmware/`, `fw-*/`, `LRAPAssets*/`, `LRAPAssests*/`, `*.zip`, `*.bin`) and `site.tar`.

## Prerequisites

- Node.js and npm. Vite 6.3 requires Node 18, 20, or 22 and later.
- Go 1.25 (`go 1.25.1` in `backend/go.mod`). The backend uses only the standard library, so no `GOPRIVATE` setting is needed.

The router is an OpenWrt MT7981 main module, ARM64. The Go programs are built for `linux/arm64` with `CGO_ENABLED=0`, so cross-compiling needs no C toolchain or OpenWrt SDK.

## Front end

```sh
npm ci
npm run lint     # ESLint
npm run build    # tsc -b && vite build: writes dist/
npm run dev      # Vite dev server on port 5173
```

`dist/` holds `index.html`, `favicon.svg`, `vite.svg` and `assets/`: that is what goes into the firmware's `/www`.

With `npm run dev` the page is served from your computer while the API is on a router. `VITE_API_BASE` overrides where the pages send API calls (`src/utils/http.ts`), and `vite.config.ts` also proxies `/api` to `http://10.10.18.1:9080`. `lrapServer` accepts calls from a page on another origin only if `cors_origin` in `/etc/config/lrapServer` (`CORS_ORIGIN`) names that origin; the default `*` means the router's own pages only. Change the development router's configuration only with the owner's authorisation (lrap-ops `AGENTS.md`).

## Backend

Build `lrapServer` for the router, from `backend/`:

```sh
cd backend
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o lrapServer .
```

```powershell
cd backend
$env:GOOS='linux'; $env:GOARCH='arm64'; $env:CGO_ENABLED='0'
go build -trimpath -o lrapServer .
```

`mtk_sta_info` is built the same way from the repository root: `go build -trimpath -o mtk_sta_info mtk_sta_info.go` with the same three variables.

Tests (they run on the development computer, no router needed; about 45 seconds):

```sh
cd backend
go vet .
go test . -count=1
```

`lrapServer` reads its settings from the environment, which `/etc/init.d/lrapServer` fills from `/etc/config/lrapServer`: `LISTEN_ADDR` (`:9080`), `UBUS_URL` (`http://127.0.0.1/ubus`), `CORS_ORIGIN` (`*`), and `TLS_CERT_FILE` and `TLS_KEY_FILE` (both empty: plain HTTP; with both set it serves HTTPS).

There is no CI workflow for this repository (lrap-ops [`docs/CI.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/CI.md)): run the lint, the build and the backend tests yourself before a pull request.

## How it reaches the firmware

The portal has no version tags: the firmware takes it from the current `main` branch, and the firmware engineer is given the files with their SHA-256. Where each file goes (lrap-ops [`docs/FIRMWARE.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/FIRMWARE.md), "Main module"):

| From this repository | In the firmware | Permissions |
|---|---|---|
| `dist/index.html`, `favicon.svg`, `vite.svg`, `dist/assets/*` | `/www/index.html`, `favicon.svg`, `vite.svg`, `/www/assets/*`. Replace `assets/` whole, leaving no old files; keep `cgi-bin` and `luci-static` | 644 |
| `lrapServer` (built above), `backend/etc/init.d/lrapServer` | `/usr/sbin/lrapServer/lrapServer`, `/etc/init.d/lrapServer` | 755 |
| `mtk_sta_info` (built above) | `/usr/bin/mtk_sta_info`, on the main module and on the antenna submodules | 755 |
| `backend/etc/uci-defaults/99-lrap-accounts` | `/etc/uci-defaults/99-lrap-accounts` | 755 |
| `backend/etc/config/lrapServer`, `rpcd`, `uhttpd` | `/etc/config/` | 644 |
| `backend/usr/share/rpcd/acl.d/*.json` (4 files) | `/usr/share/rpcd/acl.d/` | 644 |

Firmware releases then reach customers only through the platform's release bucket and the rollouts customers start (lrap-ops `Publish-Firmware.ps1`; [`docs/CLOUD.md`](https://github.com/Roobuck-RnD/lrap-ops/blob/main/docs/CLOUD.md), "Firmware releases (S3)").

## Vite template notes

The front end started from Vite's React + TypeScript template. Its notes:

This template provides a minimal setup to get React working in Vite with HMR and some ESLint rules.

Currently, two official plugins are available:

- [@vitejs/plugin-react](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react) uses [Babel](https://babeljs.io/) for Fast Refresh
- [@vitejs/plugin-react-swc](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react-swc) uses [SWC](https://swc.rs/) for Fast Refresh

### Expanding the ESLint configuration

If you are developing a production application, we recommend updating the configuration to enable type-aware lint rules:

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

You can also install [eslint-plugin-react-x](https://github.com/Rel1cx/eslint-react/tree/main/packages/plugins/eslint-plugin-react-x) and [eslint-plugin-react-dom](https://github.com/Rel1cx/eslint-react/tree/main/packages/plugins/eslint-plugin-react-dom) for React-specific lint rules:

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
