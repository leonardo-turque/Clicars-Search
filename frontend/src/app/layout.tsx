import type { Metadata } from 'next'
import './globals.css'
import Nav from '@/components/Nav'

export const metadata: Metadata = {
  title: 'Clicars Search',
  description: 'Busca de empresas e conexões de WhatsApp',
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="pt-BR">
      <body className="font-sans antialiased bg-gray-50">
        <Nav />
        {children}
      </body>
    </html>
  )
}
