// Line icons for toolbars and menus: 24×24, 2px strokes in currentColor, so
// they follow the text colour in light and dark mode and need no image files.
import { defineComponent, h, type PropType } from 'vue'

const P: Record<string, string[]> = {
  upload: ['M12 15V3', 'M7 8l5-5 5 5', 'M5 15v4a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4'],
  download: ['M12 3v12', 'M7 10l5 5 5-5', 'M5 15v4a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4'],
  updown: ['M7 20V4', 'M3 8l4-4 4 4', 'M17 4v16', 'M13 16l4 4 4-4'],
  globe: ['M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z', 'M3 12h18', 'M12 3a14 14 0 0 1 0 18', 'M12 3a14 14 0 0 0 0 18'],
  plus: ['M12 5v14', 'M5 12h14'],
  folder: ['M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z'],
  'folder-plus': ['M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z', 'M12 10v6', 'M9 13h6'],
  'folder-open': ['M3 17V7a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v1', 'M3 17l2.4-6.2A2 2 0 0 1 7.3 9.5H20a1 1 0 0 1 .9 1.4L18.6 17.7a2 2 0 0 1-1.9 1.3H5a2 2 0 0 1-2-2z'],
  file: ['M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z', 'M14 3v5h5'],
  'file-plus': ['M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z', 'M14 3v5h5', 'M12 11v6', 'M9 14h6'],
  tab: ['M3 8a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z', 'M12 12v5', 'M9.5 14.5h5'],
  window: ['M4 4h16a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z', 'M3 9h18'],
  edit: ['M4 20h4L19 9l-4-4L4 16z', 'M13.5 6.5l4 4'],
  star: ['M12 3l2.8 5.7 6.2.9-4.5 4.4 1.1 6.2L12 17.3 6.4 20.2l1.1-6.2L3 9.6l6.2-.9z'],
  search: ['M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14z', 'M20 20l-4-4'],
  link: ['M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1', 'M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1'],
  terminal: ['M4 4h16a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z', 'M7 9l3 3-3 3', 'M12 15h5'],
  disk: ['M3 13l2.6-7.1A2 2 0 0 1 7.5 4.5h9a2 2 0 0 1 1.9 1.4L21 13', 'M3 13h18v5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z', 'M7 16.5h.01', 'M11 16.5h.01'],
  more: ['M5 12h.01', 'M12 12h.01', 'M19 12h.01'],
  trash: ['M4 7h16', 'M10 11v6', 'M14 11v6', 'M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12', 'M9 7V4h6v3'],
  list: ['M9 6h11', 'M9 12h11', 'M9 18h11', 'M4.5 6h.01', 'M4.5 12h.01', 'M4.5 18h.01'],
  grid: ['M4 4h6v6H4z', 'M14 4h6v6h-6z', 'M4 14h6v6H4z', 'M14 14h6v6h-6z'],
  back: ['M19 12H5', 'M11 6l-6 6 6 6'],
  refresh: ['M20 11a8 8 0 0 0-14.6-4.5L4 8', 'M4 3v5h5', 'M4 13a8 8 0 0 0 14.6 4.5L20 16', 'M20 21v-5h-5'],
  copy: ['M9 9h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V10a1 1 0 0 1 1-1z', 'M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1'],
  cut: ['M6 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6z', 'M6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6z', 'M8.1 7.9L20 20', 'M8.1 16.1L20 4'],
  paste: ['M9 4h6v3H9z', 'M15 5h2a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h2'],
  rename: ['M4 7V5h10v2', 'M9 5v14', 'M7 19h4', 'M15 13h6', 'M18 10v9', 'M16 19h4'],
  archive: ['M3 4h18v4H3z', 'M5 8v11a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8', 'M10 12h4'],
  extract: ['M3 4h18v4H3z', 'M5 8v11a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8', 'M12 11v6', 'M9.5 14.5L12 17l2.5-2.5'],
  duplicate: ['M9 9h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V10a1 1 0 0 1 1-1z', 'M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1', 'M14 12v6', 'M11 15h6'],
  remark: ['M4 5h16a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1h-8l-4 4v-4H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z', 'M7 9h10', 'M7 13h6'],
  info: ['M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z', 'M12 11v5', 'M12 8h.01'],
  check: ['M4 4h16v16H4z', 'M8 12l3 3 5-6'],
  calc: ['M3 12h18', 'M3 12l3-3', 'M3 12l3 3', 'M21 12l-3-3', 'M21 12l-3 3'],
  lock: ['M6 11h12a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-8a1 1 0 0 1 1-1z', 'M8 11V8a4 4 0 0 1 8 0v3'],
  user: ['M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8z', 'M4 21a8 8 0 0 1 16 0'],
  warning: ['M12 4L2.5 20h19z', 'M12 10v4.5', 'M12 17.5h.01'],
  chevron: ['M6 9l6 6 6-6'],
}

export type IconName = keyof typeof P

export const Icon = defineComponent({
  name: 'LmIcon',
  props: {
    name: { type: String as PropType<IconName>, required: true },
    size: { type: [Number, String], default: '1em' },
  },
  setup(props) {
    return () =>
      h(
        'svg',
        {
          class: 'lm-icon', width: props.size, height: props.size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor',
          'stroke-width': 2, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true',
          style: 'flex: none; vertical-align: -0.15em',
        },
        (P[props.name] ?? []).map((d) => h('path', { d })),
      )
  },
})

/** For Naive dropdown / menu options: `{ label, key, icon: icon('copy') }`. */
export const icon = (name: IconName) => () => h(Icon, { name, size: 16 })
