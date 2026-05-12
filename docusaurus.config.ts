import {execSync} from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

// Resolved at build time and exposed to the landing page via `customFields`.
// Precedence: APP_VERSION env (CI override) > latest git tag > 'dev' (shallow
// clone or pre-tag checkout).
function resolveAppVersion(): string {
  if (process.env.APP_VERSION) return process.env.APP_VERSION;
  try {
    return execSync('git describe --tags --abbrev=0', {
      stdio: ['ignore', 'pipe', 'ignore'],
    })
      .toString()
      .trim();
  } catch {
    return 'dev';
  }
}

// Parsed from auth/go.mod so the landing page tracks the toolchain the auth
// binary is actually built against. We surface only MAJOR.MINOR.
function resolveGoVersion(): string {
  const modPath = path.join(__dirname, 'auth', 'go.mod');
  const src = fs.readFileSync(modPath, 'utf8');
  const match = src.match(/^go\s+(\d+\.\d+)(?:\.\d+)?/m);
  if (!match) throw new Error(`Could not parse Go version from ${modPath}`);
  return `Go ${match[1]}`;
}

const config: Config = {
  title: 'Packyard',
  tagline: 'Authenticated package distribution for LTS releases',

  future: {
    v4: true,
  },

  url: 'https://no42-org.github.io',
  baseUrl: '/packyard/',

  organizationName: 'no42-org',
  projectName: 'packyard',
  trailingSlash: false,

  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',

  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  themes: [
    '@docusaurus/theme-mermaid',
    [
      '@easyops-cn/docusaurus-search-local',
      {
        hashed: true,
        language: ['en'],
        indexDocs: true,
        indexBlog: false,
        docsRouteBasePath: '/',
      },
    ],
  ],

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  customFields: {
    appVersion: resolveAppVersion(),
    license: 'GPL-3.0',
    goVersion: resolveGoVersion(),
  },

  presets: [
    [
      'classic',
      {
        docs: {
          path: 'docs',
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
          editUrl: 'https://github.com/no42-org/packyard/edit/main/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    colorMode: {
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'Packyard',
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'gettingStarted',
          position: 'left',
          label: 'Getting Started',
        },
        {
          type: 'docSidebar',
          sidebarId: 'operations',
          position: 'left',
          label: 'Operations',
        },
        {
          type: 'docSidebar',
          sidebarId: 'reference',
          position: 'left',
          label: 'Reference',
        },
        {
          href: 'https://github.com/no42-org/packyard',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'Quick Start', to: '/getting-started/quick-start'},
            {label: 'Admin API', to: '/reference/api'},
            {label: 'Subscriber Usage', to: '/reference/subscriber-usage'},
          ],
        },
        {
          title: 'Project',
          items: [
            {
              label: 'GitHub',
              href: 'https://github.com/no42-org/packyard',
            },
            {
              label: 'Issues',
              href: 'https://github.com/no42-org/packyard/issues',
            },
            {
              label: 'Releases',
              href: 'https://github.com/no42-org/packyard/releases',
            },
          ],
        },
        {
          title: 'Legal',
          items: [
            {label: 'Imprint', to: '/imprint'},
            {label: 'Privacy', to: '/privacy'},
          ],
        },
      ],
      copyright: `© ${new Date().getFullYear()} Ronny Trommer. Licensed under GPL-3.0. Built with Docusaurus.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'yaml', 'json', 'go', 'ini', 'diff'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
