type ManagedModuleOrderFields = {
  module_id?: string
  type?: string
  port?: string
  name?: string
  ip?: string
  ipaddress?: string
}

function antennaIndex(value?: string): number | null {
  const match = String(value || '').trim().toLowerCase().match(/^antenna[:\s_-]?(\d+)$/)
  if (!match) return null
  const index = Number(match[1])
  return Number.isInteger(index) && index > 0 ? index : null
}

function lanPortIndex(value?: string): number | null {
  const match = String(value || '').trim().toLowerCase().match(/^lan(\d+)$/)
  if (!match) return null
  const index = Number(match[1])
  return Number.isInteger(index) && index > 0 ? index : null
}

function moduleDisplayOrder(module: ManagedModuleOrderFields): number {
  const moduleID = String(module.module_id || '').trim().toLowerCase()
  const moduleType = String(module.type || '').trim().toLowerCase()
  const port = String(module.port || '').trim().toLowerCase()

  if (moduleID === 'main' || moduleType === 'main' || moduleType === 'main module' || port === 'br-lan') {
    return 0
  }

  const stableIndex = antennaIndex(moduleID)
  if (stableIndex !== null) return stableIndex

  const physicalIndex = lanPortIndex(port)
  if (physicalIndex !== null) return physicalIndex

  const nameIndex = antennaIndex(module.name)
  if (nameIndex !== null) return nameIndex

  const ip = String(module.ip || module.ipaddress || '')
  const ipParts = ip.split('.')
  const lastOctet = Number(ipParts[ipParts.length - 1])
  return Number.isInteger(lastOctet) ? 1000 + lastOctet : 2000
}

function compareManagedModules(a: ManagedModuleOrderFields, b: ManagedModuleOrderFields): number {
  const orderDiff = moduleDisplayOrder(a) - moduleDisplayOrder(b)
  if (orderDiff !== 0) return orderDiff

  const nameDiff = String(a.name || '').localeCompare(String(b.name || ''))
  if (nameDiff !== 0) return nameDiff

  return String(a.ip || a.ipaddress || '').localeCompare(String(b.ip || b.ipaddress || ''))
}

export { compareManagedModules, moduleDisplayOrder }
export type { ManagedModuleOrderFields }
