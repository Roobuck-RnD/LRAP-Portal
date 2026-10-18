// Type definitions for devices (modules). The multi-device switching (module-switcher) feature was removed, and with it the
// old useCurrentModuleStore; only the Module type, still used by app-sidebar / flashfirmware,
// remains here.

type ModuleType = 'Main Module' | 'Sub Module' | 'Unknown'

type Module = {
  name: string
  ipaddress: string
  mac: string
  port: string
  logo: React.ElementType
  type: ModuleType
}

export type { Module }
