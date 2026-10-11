import { createSignal } from "solid-js"
import { r } from "~/utils/request"
import type { Resp } from "~/types"

const [archiveExtensions, setArchiveExtensions] = createSignal<string[]>([])
let pending: Promise<boolean> | undefined

export function loadArchiveExtensions(): Promise<boolean> {
  if (!pending) {
    pending = (async () => {
      try {
        const response = await r.get("/public/archive_extensions") as Resp<string[]>
        if (response.code !== 200) throw new Error(response.message)
        setArchiveExtensions(response.data)
        return true
      } catch (error) {
        pending = undefined
        throw error
      }
    })()
  }
  return pending
}

export const isArchive = (name: string): boolean => {
  name = name.toLowerCase()
  return archiveExtensions().some((v) => name.endsWith(v))
}
