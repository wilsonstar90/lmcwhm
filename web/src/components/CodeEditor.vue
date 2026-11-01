<script setup lang="ts">
// Code editor (CodeMirror 6): line numbers, syntax highlighting chosen by
// file name or language, search and replace (Ctrl/⌘+F), go to line
// (Ctrl/⌘+Alt+G), bracket matching, Ctrl/⌘+S to save and a dark theme that
// follows the system, like the rest of the panel.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDark } from '../theme'
import { basicSetup } from 'codemirror'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView, keymap, placeholder as placeholderExt } from '@codemirror/view'
import { indentWithTab } from '@codemirror/commands'
import { gotoLine, openSearchPanel } from '@codemirror/search'
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
  fontSize?: number // px, default 13
  theme?: 'auto' | 'light' | 'dark' // default: follow the panel
}>()
const emit = defineEmits<{ 'update:modelValue': [v: string]; save: []; cursor: [line: number, col: number] }>()

const host = ref<HTMLElement | null>(null)
let view: EditorView | null = null
const lang = new Compartment()
const theme = new Compartment()
const look = new Compartment()
const words = new Compartment()
const { t, locale } = useI18n()
const panelDark = useDark()
const dark = computed(() => (props.theme === 'dark' ? true : props.theme === 'light' ? false : panelDark.value))

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

// CodeMirror's own words (search, replace, go to line) in the panel's language.
const phraseKeys: Record<string, string> = {
  Find: 'find', Replace: 'replace', next: 'next', previous: 'previous', all: 'all', 'match case': 'match_case', regexp: 'regexp',
  'by word': 'by_word', replace: 'replace_one', 'replace all': 'replace_all', close: 'close', 'Go to line': 'goto_line', go: 'go',
  'current match': 'current_match', 'on line': 'on_line', 'replaced match on line $': 'replaced_on_line', 'replaced $ matches': 'replaced_n',
  'Folded lines': 'folded', 'Unfolded lines': 'unfolded', 'folded code': 'folded_code', unfold: 'unfold', 'Fold line': 'fold_line', 'Unfold line': 'unfold_line',
}
const wordsExt = () => EditorState.phrases.of(Object.fromEntries(Object.entries(phraseKeys).map(([k, v]) => [k, t(`cm.${v}`, { n: '$' })])))

const darkExt = () => (dark.value ? oneDark : [])
const lookExt = () =>
  EditorView.theme({
    '&': { height: props.height ?? '420px', fontSize: `${props.fontSize ?? 13}px`, border: '1px solid rgba(128,128,128,.25)', borderRadius: '6px' },
    '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', lineHeight: '1.5' },
    '&.cm-focused': { outline: 'none' },
    // Search / replace / go-to-line panels at the panel's own text size.
    '.cm-panels': { fontSize: '13px' },
    '.cm-panel.cm-search, .cm-panel.cm-gotoLine': { padding: '8px 36px 8px 10px', lineHeight: '2.1' },
    '.cm-panel input.cm-textfield': {
      fontSize: '13px', padding: '4px 8px', margin: '2px 6px 2px 0', borderRadius: '4px', minWidth: '200px',
      border: '1px solid rgba(128,128,128,.4)', background: 'transparent', color: 'inherit', outline: 'none',
    },
    '.cm-panel input.cm-textfield:focus': { borderColor: '#20a53a', boxShadow: '0 0 0 2px rgba(32,165,58,.18)' },
    '.cm-panel button.cm-button, .cm-panel button:not([name=close])': {
      fontSize: '13px', padding: '3px 12px', margin: '2px 4px 2px 0', borderRadius: '4px', backgroundImage: 'none',
      border: '1px solid rgba(128,128,128,.4)', background: 'transparent', color: 'inherit', cursor: 'pointer',
    },
    '.cm-panel button.cm-button:hover': { borderColor: '#20a53a', color: '#20a53a' },
    '.cm-panel label': { fontSize: '13px', marginRight: '10px', whiteSpace: 'nowrap', cursor: 'pointer' },
    '.cm-panel label input': { verticalAlign: 'middle', marginRight: '4px' },
    '.cm-panel.cm-search [name=close]': { fontSize: '20px', top: '6px', right: '8px', padding: '0 6px', cursor: 'pointer' },
  })

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
          if (u.selectionSet || u.docChanged) {
            const pos = u.state.selection.main.head
            const line = u.state.doc.lineAt(pos)
            emit('cursor', line.number, pos - line.from + 1)
          }
        }),
        look.of(lookExt()),
        words.of(wordsExt()),
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
watch(() => [props.fontSize, props.height], () => view?.dispatch({ effects: look.reconfigure(lookExt()) }))
watch(locale, () => view?.dispatch({ effects: words.reconfigure(wordsExt()) }))

// Toolbar commands of the file editor window.
defineExpose({
  search: () => view && (view.focus(), openSearchPanel(view)),
  gotoLine: () => view && (view.focus(), gotoLine(view)),
  focus: () => view?.focus(),
})
</script>

<template>
  <div ref="host" class="code-editor" />
</template>

<style scoped>
.code-editor {
  width: 100%;
}
</style>
