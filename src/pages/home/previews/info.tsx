import { Heading, Icon, Text, VStack } from "@hope-ui/solid"
import { createMemo, JSXElement, onCleanup } from "solid-js"
import { getMainColor, objStore, password, getHistoryScope } from "~/store"
import { formatDate, getFileSize, pathDir } from "~/utils"
import { useRouter } from "~/hooks"
import { DerivativeThumbnail } from "~/components/DerivativeThumbnail"
import { createDerivativeStatusBatch } from "~/utils/derivatives"
import { getIconByObj } from "~/utils/icon"

export const FileInfo = (props: { children: JSXElement }) => {
  const { pathname, isShare } = useRouter()
  const scope = createMemo(() => {
    getHistoryScope()
    const directory = pathDir(pathname())
    const singleShare = isShare() && directory === "/@s"
    const batch = createDerivativeStatusBatch(singleShare ? pathname() : directory, password())
    onCleanup(() => batch.dispose())
    return { batch, singleShare }
  })
  const icon = () => <Icon color={getMainColor()} boxSize="$20" as={getIconByObj(objStore.obj)} />
  return (
    <VStack class="fileinfo" py="$6" spacing="$6" w="$full">
      <DerivativeThumbnail
        obj={objStore.obj}
        boxSize="$20"
        objectFit="contain"
        alt={objStore.obj.name}
        fallbackErr={icon()}
        refresh={(name, signal) => {
          const current = scope()
          return current.batch.refresh(current.singleShare ? "" : name, signal)
        }}
      />
      <VStack spacing="$2">
        <Heading
          size="lg"
          css={{
            wordBreak: "break-all",
          }}
        >
          {objStore.obj.name}
        </Heading>
        <Text color="$neutral10" size="sm">
          {[
            getFileSize(objStore.obj.size),
            formatDate(objStore.obj.modified),
          ].join("  ·  ")}
        </Text>
      </VStack>
      <VStack spacing="$2">{props.children}</VStack>
    </VStack>
  )
}
