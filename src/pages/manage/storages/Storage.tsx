import {
  Badge,
  Box,
  Button,
  HStack,
  Input,
  Td,
  Text,
  Tr,
  useColorModeValue,
  VStack,
  Progress,
  ProgressIndicator,
  ProgressLabel,
} from "@hope-ui/solid"
import { createSignal, Show } from "solid-js"
import { useFetch, useRouter, useT } from "~/hooks"
import { getMainColor } from "~/store"
import { MountDetails, PEmptyResp, Resp, Storage } from "~/types"
import {
  handleResp,
  handleRespWithNotifySuccess,
  notify,
  r,
  showDiskUsage,
  usedPercentage,
  toReadableUsage,
  nearlyFull,
} from "~/utils"
import { DeletePopover } from "../common/DeletePopover"

interface StorageProps {
  storage: Storage
  refresh: () => void
}

function StorageOrder(props: StorageProps) {
  const t = useT()
  const [order, setOrder] = createSignal(String(props.storage.order))
  const [saving, save] = useFetch(async (): PEmptyResp => {
    // Fetch the full, current configuration before updating only its order.
    const current: Resp<Storage> = await r.get(
      `/admin/storage/get?id=${props.storage.id}`,
    )
    if (current.code !== 200) return current
    return r.post("/admin/storage/update", {
      ...current.data,
      order: Number(order()),
    })
  })
  return (
    <HStack
      as="form"
      spacing="$2"
      onSubmit={async (event: SubmitEvent) => {
        event.preventDefault()
        if (
          saving() ||
          !/^-?\d+$/.test(order()) ||
          !Number.isSafeInteger(Number(order())) ||
          Number(order()) === props.storage.order
        )
          return
        const resp = await save()
        handleRespWithNotifySuccess(resp, props.refresh)
      }}
    >
      <Text
        as="label"
        for={`storage-order-${props.storage.id}`}
        css={{ whiteSpace: "nowrap" }}
      >
        {t("storages.common.order")}
      </Text>
      <Input
        id={`storage-order-${props.storage.id}`}
        type="number"
        step={1}
        required
        size="sm"
        w="$20"
        value={order()}
        disabled={saving()}
        onInput={(event) => setOrder(event.currentTarget.value)}
      />
      <Button
        type="submit"
        size="sm"
        loading={saving()}
        disabled={
          !/^-?\d+$/.test(order()) ||
          !Number.isSafeInteger(Number(order())) ||
          Number(order()) === props.storage.order
        }
      >
        {t("global.save")}
      </Button>
    </HStack>
  )
}

function StorageOp(props: StorageProps) {
  const t = useT()
  const { to } = useRouter()
  const [deleteLoading, deleteStorage] = useFetch((): PEmptyResp =>
    r.post(`/admin/storage/delete?id=${props.storage.id}`),
  )
  const [enableOrDisableLoading, enableOrDisable] = useFetch((): PEmptyResp =>
    r.post(
      `/admin/storage/${props.storage.disabled ? "enable" : "disable"}?id=${
        props.storage.id
      }`,
    ),
  )
  return (
    <>
      <Button
        onClick={() => {
          to(`/@manage/storages/edit/${props.storage.id}`)
        }}
      >
        {t("global.edit")}
      </Button>
      <Button
        loading={enableOrDisableLoading()}
        colorScheme={props.storage.disabled ? "success" : "warning"}
        onClick={async () => {
          const resp = await enableOrDisable()
          handleRespWithNotifySuccess(resp, () => {
            props.refresh()
          })
        }}
      >
        {t(`global.${props.storage.disabled ? "enable" : "disable"}`)}
      </Button>
      <DeletePopover
        name={props.storage.mount_path}
        loading={deleteLoading()}
        onClick={async () => {
          const resp = await deleteStorage()
          handleResp(resp, () => {
            notify.success(t("global.delete_success"))
            props.refresh()
          })
        }}
      />
    </>
  )
}

function StorageUsage(props: { details: MountDetails | undefined }) {
  return (
    <Show when={props.details}>
      <Progress
        class="disk-usage-percentage"
        trackColor="$info3"
        rounded="$full"
        size="md"
        value={usedPercentage(props.details!)}
      >
        <ProgressIndicator
          color={nearlyFull(props.details!) ? "$danger6" : "$info6"}
          rounded="$md"
        />
        <ProgressLabel class="disk-usage-text">
          {toReadableUsage(props.details!)}
        </ProgressLabel>
      </Progress>
    </Show>
  )
}

export function StorageGridItem(props: StorageProps) {
  const t = useT()
  return (
    <VStack
      w="$full"
      spacing="$2"
      rounded="$lg"
      border="1px solid $neutral7"
      background={useColorModeValue("$neutral2", "$neutral3")()}
      // alignItems="start"
      p="$3"
      _hover={{
        border: `1px solid ${getMainColor()}`,
      }}
    >
      <HStack spacing="$2">
        <Text
          fontWeight="$medium"
          css={{
            wordBreak: "break-all",
          }}
        >
          {props.storage.mount_path}
        </Text>
        <Badge colorScheme="info">
          {t(`drivers.drivers.${props.storage.driver}`)}
        </Badge>
        <Show when={props.storage.mount_details}>
          <Badge
            colorScheme={
              nearlyFull(props.storage.mount_details!) ? "danger" : "success"
            }
          >
            {toReadableUsage(props.storage.mount_details!)}
          </Badge>
        </Show>
      </HStack>
      <HStack>
        <Text>{t("storages.common.status")}:&nbsp;</Text>
        <Box
          css={{ wordBreak: "break-all" }}
          overflowX="auto"
          innerHTML={t(
            `storages.table_fields.status.${props.storage.status}`,
            undefined,
            props.storage.status,
          )}
        />
      </HStack>
      <Text css={{ wordBreak: "break-all" }}>{props.storage.remark}</Text>
      <StorageOrder {...props} />
      <HStack spacing="$2">
        <StorageOp {...props} />
      </HStack>
    </VStack>
  )
}

export function StorageListItem(props: StorageProps) {
  const t = useT()
  return (
    <Tr>
      <Td>{props.storage.mount_path}</Td>
      <Td>{t(`drivers.drivers.${props.storage.driver}`)}</Td>
      <Td>
        <StorageOrder {...props} />
      </Td>
      <Td>
        <StorageUsage details={props.storage.mount_details} />
      </Td>
      <Td>
        {t(
          `storages.table_fields.status.${props.storage.status}`,
          undefined,
          props.storage.status,
        )}
      </Td>
      <Td>{props.storage.remark}</Td>
      <Td>
        <HStack spacing="$2">
          <StorageOp {...props} />
        </HStack>
      </Td>
    </Tr>
  )
}
