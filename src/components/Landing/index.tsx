import React, {useState} from 'react';
import useBaseUrl from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Terminal from './Terminal';
import {FEATURES, DOCS} from './data';

type HeroMeta = {appVersion: string; license: string; goVersion: string};

function Panel({title, meta, children}: {title?: string; meta?: string; children: React.ReactNode}) {
  return (
    <div className="pky-panel">
      {title && (
        <div className="pky-panel__hd">
          <span className="pky-panel__title">{title}</span>
          {meta && <span className="pky-panel__meta">{meta}</span>}
        </div>
      )}
      <div className="pky-panel__bd">{children}</div>
    </div>
  );
}

function Stat({label, value, unit}: {label: string; value: string; unit?: string}) {
  return (
    <div className="pky-stat">
      <div className="pky-stat__label">{label}</div>
      <div className="pky-stat__val">
        <span className="pky-stat__num">{value}</span>
        {unit && <span className="pky-stat__unit">{unit}</span>}
      </div>
    </div>
  );
}

function Copyable({text, prompt = '$'}: {text: string; prompt?: string}) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="pky-copy">
      <span className="pky-copy__prompt">{prompt}</span>
      <code className="pky-copy__text">{text}</code>
      <button
        type="button"
        className="pky-copy__btn"
        onClick={() => {
          navigator.clipboard?.writeText(text);
          setCopied(true);
          setTimeout(() => setCopied(false), 1200);
        }}
      >
        {copied ? 'copied' : 'copy'}
      </button>
    </div>
  );
}

function Icon({name}: {name: string}) {
  const c = {width: 18, height: 18, viewBox: '0 0 18 18', fill: 'none', stroke: 'currentColor', strokeWidth: 1} as const;
  switch (name) {
    case 'pkg':
      return (<svg {...c}><path d="M2 5l7-3 7 3v8l-7 3-7-3V5z" /><path d="M2 5l7 3 7-3M9 8v8" /></svg>);
    case 'auth':
      return (<svg {...c}><rect x="4" y="8" width="10" height="7" /><path d="M6 8V5a3 3 0 016 0v3" /></svg>);
    case 'sign':
      return (<svg {...c}><path d="M3 11l3 3 9-9" /><path d="M3 15h12" /></svg>);
    case 'promote':
      return (<svg {...c}><path d="M3 14h12M3 14l3-3M3 14l3 3" /><path d="M15 4H3M15 4l-3-3M15 4l-3 3" /></svg>);
    case 'metric':
      return (<svg {...c}><path d="M2 13 L5 9 L8 11 L11 5 L14 8 L16 7" /></svg>);
    case 'self':
      return (<svg {...c}><rect x="2" y="2" width="14" height="14" strokeDasharray="2 2" /><rect x="5" y="5" width="8" height="8" /></svg>);
    default:
      return null;
  }
}

function DocLink({to, t, h}: {to: string; t: string; h: string}) {
  return (
    <a href={to} className="pky-docs__link">
      <span className="pky-docs__link-t">{t}</span>
      <span className="pky-docs__link-h">{h}</span>
      <span className="pky-docs__link-arr">→</span>
    </a>
  );
}

export default function Landing(): JSX.Element {
  const quickStart = useBaseUrl('/getting-started/quick-start');
  const {siteConfig} = useDocusaurusContext();
  const {appVersion, license, goVersion} = siteConfig.customFields as HeroMeta;

  return (
    <main className="pky-page">
      <div className="pky-container">
        {/* hero */}
        <section className="pky-hero">
          <div className="pky-hero__grid">
            <div>
              <div className="pky-hero__eyebrow">
                <span className="pky-dot" /> {appVersion} · {license} · {goVersion}
              </div>
              <h1 className="pky-hero__title">
                Self-hosted package<br />
                distribution for the<br />
                <span className="pky-hi">LTS releases</span> you ship.
              </h1>
              <p className="pky-hero__body">
                Packyard serves <b>RPM</b>, <b>DEB</b>, and <b>OCI</b> artefacts behind subscription-key
                auth. One docker-compose stack — Traefik, a Go forward-auth service, nginx, Zot, Aptly,
                RustFS — with a GitHub Actions promotion pipeline that signs and publishes from CI.
              </p>
              <div className="pky-hero__ctas">
                <a className="pky-btn pky-btn--primary" href={quickStart}>quick start →</a>
                <a className="pky-btn" href="https://github.com/no42-org/packyard">github ↗</a>
              </div>
            </div>
            <div style={{display: 'grid', gap: 16}}>
              <Terminal />
              <div className="pky-hero__stats">
                <Stat label="formats"     value="3"     unit="rpm · deb · oci" />
                <Stat label="signing"     value="GPG"   unit="+ cosign" />
                <Stat label="auth"        value="key"   unit="per component" />
              </div>
            </div>
          </div>
        </section>

        {/* 01 quick start */}
        <section className="pky-sec">
          <div className="pky-sec__hd">
            <span className="pky-sec__num">01</span>
            <h2 className="pky-sec__title">quick start</h2>
            <span className="pky-sec__sub">stand up the stack on a docker compose host</span>
          </div>
          <div className="pky-grid-3">
            <Panel title="01 · clone" meta="git">
              <Copyable text="git clone https://github.com/no42-org/packyard.git" />
              <Copyable text="cd packyard" />
            </Panel>
            <Panel title="02 · bring up" meta="docker compose v2">
              <Copyable text={`docker compose \\
  -f compose.yml \\
  -f compose.override.ci.yml \\
  up -d`} />
            </Panel>
            <Panel title="03 · first key" meta="admin API · localhost:8080">
              <Copyable text={`curl -X POST http://localhost:8080/api/v1/keys \\
  -H 'Content-Type: application/json' \\
  -d '{"component":"core","label":"dev-key"}'`} />
            </Panel>
          </div>
        </section>

        {/* 02 features */}
        <section className="pky-sec">
          <div className="pky-sec__hd">
            <span className="pky-sec__num">02</span>
            <h2 className="pky-sec__title">what's in the box</h2>
            <span className="pky-sec__sub">six pillars</span>
          </div>
          <div className="pky-features">
            {FEATURES.map((f, i) => (
              <div key={f.title} className="pky-panel">
                <div className="pky-panel__hd">
                  <span className="pky-panel__meta" style={{color: 'var(--pky-fg-mute)'}}>{String(i + 1).padStart(2, '0')}</span>
                </div>
                <div className="pky-panel__bd">
                  <div className="pky-feat__ic"><Icon name={f.icon} /></div>
                  <div className="pky-feat__t">{f.title}</div>
                  <p className="pky-feat__b">{f.body}</p>
                </div>
              </div>
            ))}
          </div>
        </section>

        {/* 03 docs map */}
        <section className="pky-sec">
          <div className="pky-sec__hd">
            <span className="pky-sec__num">03</span>
            <h2 className="pky-sec__title">documentation map</h2>
            <span className="pky-sec__sub">jump in</span>
          </div>
          <div className="pky-grid-3">
            {DOCS.map(d => (
              <Panel key={d.group} title={d.group.toLowerCase()}>
                <p className="pky-docs__body">{d.body}</p>
                <ul className="pky-docs__links">
                  {d.links.map(l => (
                    <li key={l.h}><DocLinkBU t={l.t} h={l.h} /></li>
                  ))}
                </ul>
              </Panel>
            ))}
          </div>
        </section>

        {/* cta */}
        <section className="pky-sec" style={{borderBottom: 'none'}}>
          <Panel>
            <div className="pky-cta">
              <div>
                <div className="pky-cta__eye">→ get started</div>
                <h2 className="pky-cta__t">Run your own authenticated&nbsp;package&nbsp;mirror.</h2>
                <p className="pky-cta__b">GPL-3.0. No SaaS, no per-subscriber meter. A docker-compose stack and a CI workflow you read end-to-end in an afternoon.</p>
              </div>
              <div className="pky-cta__r">
                <a className="pky-btn pky-btn--primary" href={quickStart}>quick start →</a>
                <a className="pky-btn" href="https://github.com/no42-org/packyard">github ↗</a>
              </div>
            </div>
          </Panel>
        </section>
      </div>
    </main>
  );
}

// Separate component so we can call the `useBaseUrl` hook per link (hooks cannot
// run inside a .map callback's arrow if we want SSR-safe base-url resolution
// under Docusaurus — this defers it to render time).
function DocLinkBU({t, h}: {t: string; h: string}) {
  const to = useBaseUrl(h);
  return <DocLink to={to} t={t} h={h} />;
}
