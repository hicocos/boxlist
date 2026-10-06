import { Heading, Icon, Image, Text, VStack } from "@hope-ui/solid"
import { JSXElement } from "solid-js"
import { getMainColor, objStore } from "~/store"
import { formatDate, getFileSize } from "~/utils"
import { getIconByObj } from "~/utils/icon"

export const FileInfo = (props: { children: JSXElement }) => {
  return (
    <VStack class="fileinfo" py="$6" spacing="$6" w="$full">
      <Image
        boxSize="$20"
        fallback={
          <Icon
            color={getMainColor()}
            boxSize="$20"
            as={getIconByObj(objStore.obj)}
          />
        }
        src={objStore.obj.thumb}
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
