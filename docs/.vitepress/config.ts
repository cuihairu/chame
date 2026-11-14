import { defineConfig } from 'vitepress'

// Deployed at https://cuihairu.github.io/chame/
const base = '/chame/'

export default defineConfig({
  base,
  // The public site stays focused (home, quick start, architecture, glossary,
  // config/API, and the design decisions + asset/ledger boundary contract).
  // The full spec, detailed ADRs, review notes, and operational documents
  // remain repository markdown so the site is not an encyclopedia.
  srcExclude: [
    'design/chame-spec.md',
    'design/game-asset-ownership-and-ledger-boundary.md',
    '审计-文档一致性.md',
    'decisions/**',
    'review/**',
    'production-observability.md',
    'internal-controls.md',
    'deployment-write-model.md',
    'vulnerability-review.md',
  ],
  title: 'Chame',
  // Bilingual site: root = English, Chinese pages live in docs/zh/ -> /zh/.
  // The top-level `locales` map (not themeConfig.locales) is what makes
  // VitePress emit a per-route <html lang> and localized site metadata; nav
  // and sidebar live in each locale's themeConfig.
  locales: {
    root: {
      label: 'English',
      lang: 'en-US',
      description: 'Go game backend framework abstracting blockchains behind one runtime',
      themeConfig: {
        nav: [
          { text: 'Home', link: '/' },
          { text: 'Quick Start', link: '/quickstart' },
          { text: 'Architecture', link: '/analysis/source-layer-analysis.en' },
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
                { text: 'Architecture', link: '/analysis/source-layer-analysis.en' },
                { text: 'Config / API', link: '/config' },
              ],
            },
            {
              text: 'Design',
              items: [
                { text: 'Architecture Decisions', link: '/design/decisions' },
                {
                  text: 'Asset Ownership & Ledger Boundary',
                  link: '/design/game-asset-ownership-and-ledger-boundary.en',
                },
              ],
            },
          ],
        },
      },
    },
    zh: {
      label: '中文',
      lang: 'zh-CN',
      description: '把区块链抽象在统一运行时之后的 Go 游戏后端框架',
      themeConfig: {
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
  // Evidence links in analysis/design pages point at repository sources (.go
  // files) and at markdown kept out of the site (detailed ADRs, spec, ops docs).
  // They are valid repo-relative references, not site routes, so they are
  // excluded from the dead-link check rather than rewritten.
  ignoreDeadLinks: [
    /\.go(#L\d+)?$/,
    /\.\.\/\.\.\//,
    /\.\.\/(decisions|design)\//,
    /\.\.\/(deployment-write-model|internal-controls|production-observability|README\.zh|todo)/,
  ],
  head: [['link', { rel: 'icon', type: 'image/jpeg', href: `${base}images/logo.jpg` }]],
  themeConfig: {
    logo: `${base}images/logo.jpg`,
    repo: 'cuihairu/chame',
    socialLinks: [{ icon: 'github', link: 'https://github.com/cuihairu/chame' }],
    search: { provider: 'local' },
  },
})
