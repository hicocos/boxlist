import { createEffect, createSignal, on, onCleanup } from "solid-js"
import { useRouter } from "~/hooks"
import { getHistoryScope, password } from "~/store"
import type { Derivatives } from "~/types/derivatives"
import { createDerivativeStatusBatch } from "~/utils/derivatives"

/** Lives only as long as this directory page; paths retain /@s share scopes. */
export function useDirectoryThumbnails() {
  const { pathname } = useRouter()
  let batch = createDerivativeStatusBatch(pathname(), password())
  const [revision, setRevision] = createSignal(0)
  const values = new Map<string, Derivatives>()
  createEffect(on([pathname, password, getHistoryScope], () => {
    batch.dispose()
    batch = createDerivativeStatusBatch(pathname(), password())
    values.clear()
    setRevision(value => value + 1)
  }))
  onCleanup(() => batch.dispose())
  return {
    revision,
    derivatives(name: string) { revision(); return values.get(name) },
    refresh(name: string, signal?: AbortSignal) { return batch.refresh(name, signal) },
    async refreshVisible(names: string[], signal?: AbortSignal) {
      const owner = batch
      const results = await Promise.all(names.map(name => owner.refresh(name, signal)))
      if (owner !== batch || signal?.aborted) return false
      if (results.some(result => result.code !== 200)) return false
      names.forEach((name, index) => values.set(name, results[index].derivatives || {}))
      setRevision(value => value + 1)
      return true
    },
  }
}
