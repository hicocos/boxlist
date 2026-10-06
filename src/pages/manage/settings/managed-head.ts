import { Resp, SettingItem } from "~/types"
import { r } from "~/utils"

// Appearance blocks are hidden in the raw-head editor and merged from the
// latest saved head, so that editing unrelated HTML cannot reset icons.
import { editableManagedHead, mergeIconManagedHead } from "~/utils/icon-policy"
export const editableHead = editableManagedHead
export const mergeManagedHead = mergeIconManagedHead

export const editableSettings = (items: SettingItem[]) =>
  items.map((item) =>
    item.key === "customize_head"
      ? { ...item, value: editableHead(item.value) }
      : item,
  )

export const prepareSettingsSave = async (items: SettingItem[]) => {
  if (!items.some((item) => item.key === "customize_head")) return items
  // Fetch at save time: another tab may have changed appearance while this
  // editor was open. Never restore a stale snapshot of the hidden blocks.
  const resp: Resp<SettingItem> = await r.get(
    "/admin/setting/get?key=customize_head",
  )
  if (resp.code !== 200) throw new Error(resp.message)
  return items.map((item) =>
    item.key === "customize_head"
      ? { ...item, value: mergeManagedHead(item.value, resp.data.value) }
      : item,
  )
}

export const verifyHeadSave = async (items: SettingItem[]) => {
  const head = items.find((item) => item.key === "customize_head")
  if (!head) return
  const resp: Resp<SettingItem> = await r.get(
    "/admin/setting/get?key=customize_head",
  )
  if (resp.code !== 200 || resp.data.value !== head.value) {
    throw new Error("Custom head was not saved")
  }
}
