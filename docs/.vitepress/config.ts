import { defineConfig } from 'vitepress'

// Deployed at https://cuihairu.github.io/chame/
const base = '/chame/'

export default defineConfig({
  base,
  // The public site is minimal (home, quick start, architecture, glossary,
  // and config/API). Detailed specs, decisions, review notes, and operational
  // documents remain repository markdown so the site is not an encyclopedia.
  srcExclude: [
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
          { text: 'Architecture', link: '/analysis/source-layer-analysis' },
          { text: 'Config / API', link: '/config' },
        ],
        sidebar: {
          '/': [
            {
              text: 'Guide',
              items: [
                { text: 'Introduction', link: '/' },
                { text: 'Quick Start', link: '/quickstart' },
                { text: 'Glossary', link: '/glossary.en' },
                { text: 'Architecture', link: '/analysis/source-layer-analysis' },
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
          { text: '架构', link: '/zh/analysis/source-layer-analysis' },
          { text: '配置 / API', link: '/zh/config' },
        ],
        sidebar: {
          '/zh/': [
            {
              text: '指南',
              items: [
                { text: '介绍', link: '/zh/' },
                { text: '快速开始', link: '/zh/quickstart' },
                { text: '术语表', link: '/zh/glossary' },
                { text: '架构', link: '/zh/analysis/source-layer-analysis' },
                { text: '配置 / API', link: '/zh/config' },
              ],
            },
          ],
        },
      },
    },
  },
})
