// The panel's own confirm / prompt (instead of the browser's): styled like
// the rest of the panel, translated, and usable on phones.
import { h, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NInput, useDialog } from 'naive-ui'

export function useAsk() {
  const dialog = useDialog()
  const { t } = useI18n()

  /** Resolves true when the user confirms. danger: a red button (delete, discard). */
  function confirm(content: string, opts: { title?: string; ok?: string; danger?: boolean } = {}): Promise<boolean> {
    return new Promise((resolve) => {
      let done = false
      const finish = (v: boolean) => {
        if (!done) {
          done = true
          resolve(v)
        }
      }
      dialog[opts.danger ? 'error' : 'warning']({
        title: opts.title ?? t('common.confirm'),
        content,
        positiveText: opts.ok ?? t('common.confirm'),
        negativeText: t('common.cancel'),
        onPositiveClick: () => finish(true),
        onNegativeClick: () => finish(false),
        onClose: () => finish(false),
        onMaskClick: () => finish(false),
        onAfterLeave: () => finish(false),
      })
    })
  }

  /** Resolves the trimmed text, or null when cancelled or empty. */
  function prompt(title: string, opts: { value?: string; placeholder?: string } = {}): Promise<string | null> {
    return new Promise((resolve) => {
      const v = ref(opts.value ?? '')
      let done = false
      const finish = (ok: boolean) => {
        if (done) return
        done = true
        const s = v.value.trim()
        resolve(ok && s ? s : null)
      }
      const d = dialog.create({
        title,
        showIcon: false,
        content: () =>
          h(NInput, {
            value: v.value,
            placeholder: opts.placeholder,
            autofocus: true,
            'onUpdate:value': (x: string) => (v.value = x),
            onKeyup: (e: KeyboardEvent) => {
              if (e.key === 'Enter') {
                finish(true)
                d.destroy()
              }
            },
          }),
        positiveText: t('common.confirm'),
        negativeText: t('common.cancel'),
        onPositiveClick: () => finish(true),
        onNegativeClick: () => finish(false),
        onClose: () => finish(false),
        onMaskClick: () => finish(false),
        onAfterLeave: () => finish(false),
      })
    })
  }

  return { confirm, prompt }
}
