import { Show } from "solid-js"
import { Markdown } from "~/components"
import { useRouter, useTitle } from "~/hooks"
import { getSetting } from "~/store"
import { notify } from "~/utils"
import { Body } from "./Body"

import { Header } from "./header/Header"
import { Toolbar } from "./toolbar/Toolbar"
import { Search } from "./folder/Search"

const announcementSessionKey = "openlist-announcement-shown"
let announcementShown = false

const Index = () => {
  const { isShare } = useRouter()
  useTitle(getSetting("site_title"))
  const announcement = getSetting("announcement")
  if (announcement && !announcementShown) {
    try {
      announcementShown = sessionStorage.getItem(announcementSessionKey) === "true"
    } catch {
      // Keep navigation deduplication when browser storage is unavailable.
    }
    if (!announcementShown) {
      notify.render(<Markdown children={announcement} />)
      announcementShown = true
      try {
        sessionStorage.setItem(announcementSessionKey, "true")
      } catch {
        // The in-memory flag still covers this document's lifetime.
      }
    }
  }
  return (
    <>
      <Header />
      <Toolbar />
      <Body />
      <Show when={!isShare() && getSetting("search_index") !== "none"}>
        <Search />
      </Show>
    </>
  )
}

export default Index
