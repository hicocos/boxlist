import { VStack } from "@hope-ui/solid"
import { Nav } from "./Nav"
import { Obj } from "./Obj"
import { Container } from "./Container"
import { Sidebar } from "./Sidebar"
import { Show, lazy, Suspense } from "solid-js"
import { primaryReady } from "~/app/primary-ready"

export const Body = () => {
  const Readme = lazy(() => import("./Readme").then(module => ({ default: module.Readme })))
  return (
    <Container>
      <VStack
        class="body"
        mt="$1"
        py="$2"
        px="2%"
        minH="80vh"
        w="$full"
        gap="$4"
      >
        <Suspense><Show when={primaryReady()}><Readme files={["header.md", "top.md", "index.md"]} fromMeta="header" /></Show></Suspense>
        <Nav />
        <Obj />
        <Suspense><Show when={primaryReady()}><Readme
          files={["readme.md", "footer.md", "bottom.md"]}
          fromMeta="readme"
        /></Show></Suspense>
        <Sidebar />
      </VStack>
    </Container>
  )
}
