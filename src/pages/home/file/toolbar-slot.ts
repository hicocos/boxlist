import { Accessor, createContext, useContext } from "solid-js"

export type FileToolbarSlotContextValue = {
  mount: Accessor<HTMLDivElement | undefined>
  hasExtras: Accessor<boolean>
  setHasExtras: (has: boolean) => void
}

export const FileToolbarSlotContext =
  createContext<FileToolbarSlotContextValue>()

export const useFileToolbarSlot = () => useContext(FileToolbarSlotContext)
