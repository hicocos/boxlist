import { currentLang } from "~/app/i18n"

/** Copy for the custom UX layer; follows the existing reactive language setting. */
export const uxText = (
  simplified: string,
  english: string,
  traditional?: string,
) => {
  const language = String(currentLang())
  if (language === "zh-TW") return traditional ?? simplified
  return language.startsWith("zh") ? simplified : english
}
