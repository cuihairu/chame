import { defineConfig } from 'vitepress'

// Deployed at https://cuihairu.github.io/chame/
const base = '/chame/'

export default defineConfig({
  base,
  // The site is deliberately minimal (home + quick start + config/API). The
  // spec, decisions, analysis and review docs stay as repository markdown
  // (browsable on GitHub); exclude them from page scanning so the build stays
  // green and the site is not an encyclopedia.
  srcExclude: [
    'analysis/**',
    'design/**',
    '审计-文档一致性.md',
    'decisions/**',
    'review/**',
    'production-observability.md',
    'internal-controls.md',
    'deployment-write-model.md',
    'vulnerability-review.md',
  ],
  title: 'Chame',
  description: 'Go game backend framework abstracting blockchains behind one runtime',
  head: [['link', { rel: 'icon', type: 'image/jpeg', href: `${base}images/logo.jpg` }]],
  themeConfig: {
    logo: `${base}images/logo.jpg`,
    repo: 'cuihairu/chame',
    socialLinks: [{ icon: 'github', link: 'https://github.com/cuihairu/chame' }],
    search: { provider: 'local' },
    locales: {
      root: {
        label: 'English',
        lang: 'en',
        nav: [
          { text: 'Home', link: '/' },
          { text: 'Quick Start', link: '/quickstart' },
          { text: 'Config / API', link: '/config' },
        ],
        sidebar: {
          '/': [
            {
              text: 'Guide',
              items: [
                { text: 'Introduction', link: '/' },
                { text: 'Quick Start', link: '/quickstart' },
                { text: 'Config / API', link: '/config' },
              ],
            },
          ],
        },
      },
      zh: {
        label: '中文',
        lang: 'zh-CN',
        nav: [
          { text: '首页', link: '/zh/' },
          { text: '快速开始', link: '/zh/quickstart' },
          { text: '配置 / API', link: '/zh/config' },
        ],
        sidebar: {
          '/zh/': [
            {
              text: '指南',
              items: [
                { text: '介绍', link: '/zh/' },
                { text: '快速开始', link: '/zh/quickstart' },
                { text: '配置 / API', link: '/zh/config' },
              ],
            },
          ],
        },
      },
    },
  },
})
