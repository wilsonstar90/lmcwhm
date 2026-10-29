<script setup lang="ts">
// Code editor (CodeMirror 6): line numbers, syntax highlighting chosen by
// file name or language, search and replace (Ctrl/⌘+F), go to line
// (Ctrl/⌘+Alt+G), bracket matching, Ctrl/⌘+S to save and a dark theme that
// follows the system, like the rest of the panel.
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useDark } from '../theme'
import { basicSetup } from 'codemirror'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView, keymap, placeholder as placeholderExt } from '@codemirror/view'
import { indentWithTab } from '@codemirror/commands'
import { LanguageDescription } from '@codemirror/language'
import { languages } from '@codemirror/language-data'
import { oneDark } from '@codemirror/theme-one-dark'

const props = defineProps<{
  modelValue: string
  // A file name ("nginx.conf", "docker-compose.yml") or a language name ("shell", "ini", "yaml").
  language?: string
  height?: string // CSS height, default 420px
  readonly?: boolean
  placeholder?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [v: string]; save: [] }>()

const host = ref<HTMLElement | null>(null)
let view: EditorView | null = null
const lang = new Compartment()
const theme = new Compartment()
const dark = useDark()

// Names the panel uses that CodeMirror knows under another name.
const aliases: Record<string, string> = { ini: 'properties', conf: 'nginx', bash: 'shell', sh: 'shell', env: 'properties', cnf: 'properties', yml: 'yaml' }

async function loadLanguage(name?: string) {
  if (!view || !name) return
  const key = aliases[name.toLowerCase()] ?? name
  const desc = LanguageDescription.matchFilename(languages, name) ?? LanguageDescription.matchLanguageName(languages, key, true) ??
    (/\.(conf|cnf|ini)$/i.test(name) ? LanguageDescription.matchLanguageName(languages, /\.conf$/i.test(name) ? 'nginx' : 'properties') : null) ??
    (/(^|\/)(\.env|\.user\.ini|\.htaccess)$/i.test(name) ? LanguageDescription.matchLanguageName(languages, 'properties') : null)
  if (!desc) {
    view.dispatch({ effects: lang.reconfigure([]) })
    return
  }
  const support = await desc.load()
  view?.dispatch({ effects: lang.reconfigure(support) })
}

const darkExt = () => (dark.value ? oneDark : [])

onMounted(() => {
  view = new EditorView({
    parent: host.value!,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        basicSetup,
        keymap.of([indentWithTab, { key: 'Mod-s', preventDefault: true, run: () => (emit('save'), true) }]),
        lang.of([]),
        theme.of(darkExt()),
        EditorState.readOnly.of(!!props.readonly),
        props.placeholder ? placeholderExt(props.placeholder) : [],
        EditorView.updateListener.of((u) => {
          if (u.docChanged) emit('update:modelValue', u.state.doc.toString())
        }),
        EditorView.theme({
          '&': { height: props.height ?? '420px', fontSize: '13px', border: '1px solid rgba(128,128,128,.25)', borderRadius: '6px' },
          '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', lineHeight: '1.5' },
          '&.cm-focused': { outline: 'none' },
        }),
      ],
    }),
  })
  loadLanguage(props.language)
})
onBeforeUnmount(() => view?.destroy())

// Outside changes (a file loaded, a preset applied) replace the document.
watch(() => props.modelValue, (v) => {
  if (view && v !== view.state.doc.toString()) view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } })
})
watch(() => props.language, (l) => loadLanguage(l))
watch(dark, () => view?.dispatch({ effects: theme.reconfigure(darkExt()) }))
</script>

<template>
  <div ref="host" class="code-editor" />
</template>

<style scoped>
.code-editor {
  width: 100%;
}
</style>
