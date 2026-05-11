import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

// Three independent sidebars, one per top-level section. The navbar items in
// docusaurus.config.ts select which sidebar to render via `sidebarId`.
const sidebars: SidebarsConfig = {
  gettingStarted: [
    {
      type: 'category',
      label: 'Getting Started',
      collapsed: false,
      items: [
        'getting-started/quick-start',
      ],
    },
  ],

  operations: [
    {
      type: 'category',
      label: 'Operations',
      collapsed: false,
      items: [
        'ops/production-deployment',
        'ops/release-runbook',
        'ops/restore-keystore',
        'ops/manual-test-plan',
        'ops/troubleshooting',
      ],
    },
  ],

  reference: [
    {
      type: 'category',
      label: 'Reference',
      collapsed: false,
      items: [
        'reference/architecture',
        'reference/subscriber-usage',
        'reference/api',
        'reference/promotion-pipeline',
        'reference/observability',
        'reference/configuration',
      ],
    },
  ],
};

export default sidebars;
