// 设备(模块)的类型定义。多设备切换(module-switcher)特性已移除,原来的
// useCurrentModuleStore 一并删掉;这里只保留仍被 app-sidebar / flashfirmware 使用的
// Module 类型。

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
