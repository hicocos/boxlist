export interface FileSortState {
  orderBy: "name" | "size" | "modified"
  reverse: boolean
}
export function parseFileSortState(value: string | null): FileSortState | null {
  if (!value) return null
  try {
    const state = JSON.parse(value)
    return state &&
      ["name", "size", "modified"].includes(state.orderBy) &&
      typeof state.reverse === "boolean"
      ? { orderBy: state.orderBy, reverse: state.reverse }
      : null
  } catch {
    return null
  }
}
