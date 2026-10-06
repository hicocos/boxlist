/** Pure selection operations. Targets are object identities, never filtered indices. */
export interface SelectionState<T> {
  anchor: T | null
  baseline: Set<T> | null
}
export const emptySelectionState = <T>(): SelectionState<T> => ({
  anchor: null,
  baseline: null,
})

export function changeSelection<T>(
  visible: readonly T[],
  selected: ReadonlySet<T>,
  state: SelectionState<T>,
  target: T,
  checked: boolean,
  options: { one?: boolean; range?: boolean } = {},
): { selected: Set<T>; state: SelectionState<T> } {
  const allowed = new Set(visible)
  const current = new Set([...selected].filter((item) => allowed.has(item)))
  const index = visible.indexOf(target)
  if (index < 0) return { selected: current, state }
  if (options.one) {
    return {
      selected: new Set(checked ? [target] : []),
      state: { anchor: checked ? target : null, baseline: null },
    }
  }
  if (options.range) {
    let anchor: T | null =
      state.anchor !== null && allowed.has(state.anchor) ? state.anchor : null
    if (anchor === null) {
      anchor =
        visible.reduce<T | null>((nearest, item, i) => {
          if (!current.has(item)) return nearest
          return nearest === null ||
            Math.abs(i - index) < Math.abs(visible.indexOf(nearest) - index)
            ? item
            : nearest
        }, null) ?? target
    }
    const baseline = state.baseline ?? current
    const next = new Set([...baseline].filter((item) => allowed.has(item)))
    const start = visible.indexOf(anchor)
    for (let i = Math.min(start, index); i <= Math.max(start, index); i++) {
      if (checked) next.add(visible[i])
      else next.delete(visible[i])
    }
    return { selected: next, state: { anchor, baseline } }
  }
  if (checked) current.add(target)
  else current.delete(target)
  return {
    selected: current,
    state: { anchor: checked ? target : null, baseline: null },
  }
}

export function visibleSelection<T>(
  loaded: readonly T[],
  visible: readonly T[],
) {
  const loadedSet = new Set(loaded)
  return [...new Set(visible)].filter((item) => loadedSet.has(item))
}

export function nativeLinkClick(event: {
  button: number
  detail: number
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
}) {
  return (
    event.detail === 0 ||
    event.button !== 0 ||
    event.ctrlKey ||
    event.metaKey ||
    event.altKey
  )
}
