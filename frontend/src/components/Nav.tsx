'use client'

import Link from 'next/link'
import { usePathname } from 'next/navigation'

const tabs = [
  { href: '/', label: 'Busca', shortLabel: 'Buscar', icon: '⌕' },
  { href: '/whatsapp', label: 'Números', shortLabel: 'Números', icon: '◉' },
  { href: '/campanhas', label: 'Campanhas', shortLabel: 'Envios', icon: '↗' },
  { href: '/protecao', label: 'Proteção', shortLabel: 'Saúde', icon: '⬡' },
]

export default function Nav() {
  const pathname = usePathname()

  const isActive = (href: string) => href === '/' ? pathname === '/' : pathname.startsWith(href)

  return (
    <>
      <header className="topbar">
        <div className="topbar__inner">
          <Link href="/" className="brand" aria-label="Clicars Search — início">
            <span className="brand__mark" aria-hidden="true">C</span>
            <span className="brand__copy">
              <strong>Clicars</strong>
              <small>Search</small>
            </span>
          </Link>

          <nav className="desktop-nav" aria-label="Navegação principal">
            {tabs.map((tab) => {
              const active = isActive(tab.href)
              return (
                <Link
                  key={tab.href}
                  href={tab.href}
                  aria-current={active ? 'page' : undefined}
                  className={`desktop-nav__item ${active ? 'is-active' : ''}`}
                >
                  <span aria-hidden="true">{tab.icon}</span>
                  {tab.label}
                </Link>
              )
            })}
          </nav>

          <div className="topbar__status" title="Operação protegida por controles de conformidade">
            <span aria-hidden="true" />
            Operação responsável
          </div>
        </div>
      </header>

      <nav className="mobile-nav" aria-label="Navegação principal">
        {tabs.map((tab) => {
          const active = isActive(tab.href)
          return (
            <Link
              key={tab.href}
              href={tab.href}
              aria-current={active ? 'page' : undefined}
              className={`mobile-nav__item ${active ? 'is-active' : ''}`}
            >
              <span className="mobile-nav__icon" aria-hidden="true">{tab.icon}</span>
              <span>{tab.shortLabel}</span>
            </Link>
          )
        })}
      </nav>
    </>
  )
}
