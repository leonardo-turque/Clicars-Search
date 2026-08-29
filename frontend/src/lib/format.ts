export const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'

export function formatPhone(raw: string): string {
  if (!raw) return 'Número desconhecido'
  const d = raw.replace(/\D/g, '')
  if (d.length >= 12 && d.startsWith('55')) {
    const ddd = d.slice(2, 4)
    const rest = d.slice(4)
    return `+55 (${ddd}) ${rest.slice(0, rest.length - 4)}-${rest.slice(-4)}`
  }
  return d ? `+${d}` : 'Número desconhecido'
}

export function digitsOnly(raw: string): string {
  return (raw || '').replace(/\D/g, '')
}
