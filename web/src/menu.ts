import { ref } from 'vue'
import { get, put } from './api/client'

// The main menu's order and hidden entries (Panel settings → Interface).
export interface MenuConfig { order: string[]; hidden: string[] }

export const menuConfig = ref<MenuConfig>({ order: [], hidden: [] })
export const menuKeys = ref<string[]>([])

export async function loadMenu() {
  try {
    const r = await get<{ menu: MenuConfig; keys: string[] }>('/ui/menu')
    menuConfig.value = r.menu
    menuKeys.value = r.keys
  } catch {
    /* keep the default order */
  }
}

export async function saveMenu(m: MenuConfig) {
  const r = await put<{ menu: MenuConfig }>('/ui/menu', m)
  menuConfig.value = r.menu
}

// arrange returns keys in the saved order; keys it does not know yet go last.
export function arrange(keys: string[], m: MenuConfig): string[] {
  const pos = new Map(m.order.map((k, i) => [k, i]))
  return [...keys].sort((a, b) => (pos.get(a) ?? 1e6 + keys.indexOf(a)) - (pos.get(b) ?? 1e6 + keys.indexOf(b)))
}
