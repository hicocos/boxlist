import { Markdown } from "~/components"
import { useTitle } from "~/hooks"
import { getSetting } from "~/store"
import { notify } from "~/utils"
import { Body } from "./Body"

import { Header } from "./header/Header"
import { Toolbar } from "./toolbar/Toolbar"

const announcementSessionKey = "openlist-announcement-shown"
let announcementShown = false

const Index = () => {
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
    </>
  )
}

export default Index
