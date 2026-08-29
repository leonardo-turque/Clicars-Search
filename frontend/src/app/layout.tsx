import type { Metadata, Viewport } from 'next'
import './globals.css'
import Nav from '@/components/Nav'

export const metadata: Metadata = {
  metadataBase: new URL(process.env.NEXT_PUBLIC_SITE_URL || 'http://localhost:3000'),
  title: {
    default: 'Clicars Search',
    template: '%s · Clicars Search',
  },
  description: 'Prospecção organizada, gestão de contatos e campanhas com proteção anti-ban.',
  openGraph: {
    title: 'Clicars Search',
    description: 'Empresas certas. Conversas no ritmo certo.',
    locale: 'pt_BR',
    type: 'website',
    images: [{ url: '/og.png', width: 1734, height: 909, alt: 'Clicars Search' }],
  },
  twitter: {
    card: 'summary_large_image',
    title: 'Clicars Search',
    description: 'Empresas certas. Conversas no ritmo certo.',
    images: ['/og.png'],
  },
}

export const viewport: Viewport = {
  width: 'device-width',
  initialScale: 1,
  viewportFit: 'cover',
  themeColor: '#f5f7fb',
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="pt-BR">
      <body className="antialiased">
        <Nav />
        {children}
      </body>
    </html>
  )
}
