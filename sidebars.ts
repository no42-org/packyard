import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

// Three independent sidebars, one per top-level section. The navbar items in
// docusaurus.config.ts select which sidebar to render via `sidebarId`.
const sidebars: SidebarsConfig = {
  // Single page, so no category wrapper: the navbar item already carries the
  // "Getting Started" label and a category would repeat it in the mobile menu.
  gettingStarted: ['getting-started/quick-start'],

  operations: [
    {
      type: 'category',
      label: 'Operations',
      collapsed: false,
      items: [
        'ops/production-deployment',
        'ops/operator-onboarding',
        'ops/admin-migration-runbook',
        'ops/release-runbook',
        'ops/upstream-release-dispatch',
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
