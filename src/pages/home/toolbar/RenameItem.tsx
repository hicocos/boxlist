import { createMemo, createSignal, For, Show } from "solid-js"
import { RenameObj } from "~/types"
import { changedNameParts } from "~/utils/file-names"
import type { RenameError } from "~/utils/file-renames"
import { uxText } from "~/utils/ux"
import "../folder/file-rows.css"

export const RenameItem = (props: {
  obj: RenameObj
  index: number
  errors?: RenameError[]
  unchanged?: boolean
}) => {
  const [expanded, setExpanded] = createSignal(false)
  const diff = createMemo(() =>
    changedNameParts(props.obj.src_name, props.obj.new_name),
  )
  const message = (error: RenameError) =>
    ({
      empty: uxText("新名称不能为空", "New name is empty", "新名稱不可為空"),
      invalid: uxText(
        "名称包含非法字符或路径",
        "Invalid characters or path in name",
        "名稱含有無效字元或路徑",
      ),
      duplicate: uxText(
        "多个项目生成了相同名称",
        "Multiple items have the same destination",
        "多個項目產生相同名稱",
      ),
      conflict: uxText(
        "与已有名称冲突",
        "Conflicts with an existing name",
        "與既有名稱衝突",
      ),
      missing: uxText(
        "原项目已不存在",
        "Source item is no longer present",
        "原項目已不存在",
      ),
    })[error]
  return (
    <details
      class="ux-rename-preview"
      onToggle={(e) => setExpanded(e.currentTarget.open)}
    >
      <summary>
        {props.index + 1}.{" "}
        {expanded()
          ? uxText("收起完整名称", "Collapse full names", "收合完整名稱")
          : uxText("展开完整名称", "Expand full names", "展開完整名稱")}
        <Show when={props.unchanged}>
          {" "}
          — {uxText("不变，跳过", "Unchanged, skipped", "不變，略過")}
        </Show>
        <div class="ux-rename-columns">
          <div>
            <small>{uxText("原名称", "Before", "原名稱")}</small>
            <span class="ux-rename-name">
              {diff().prefix}
              <del>{diff().removed}</del>
              {diff().suffix}
            </span>
          </div>
          <div>
            <small>{uxText("新名称", "After", "新名稱")}</small>
            <span class="ux-rename-name">
              {diff().prefix}
              <mark>{diff().added}</mark>
              {diff().suffix}
            </span>
          </div>
        </div>
        <For each={props.errors}>
          {(error) => <p class="ux-rename-error">{message(error)}</p>}
        </For>
      </summary>
    </details>
  )
}
