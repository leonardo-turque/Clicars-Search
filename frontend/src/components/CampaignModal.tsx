'use client'

import { useState, useEffect, useCallback, useRef } from 'react'

type WAStatus = 'CONNECTING' | 'CONNECTED' | 'DISCONNECTED'

interface WhatsAppSession {
  id: string
  phone_number: string
  status: WAStatus
}

type CampaignStatus = 'PENDING' | 'RUNNING' | 'COMPLETED'

interface Campaign {
  id: string
  status: CampaignStatus
  total: number
  sent: number
  failed: number
  progress: string
}

const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'
const POLL_MS = 2500
const MAX_MESSAGE = 1000

function Spinner({ className = 'h-4 w-4' }: { className?: string }) {
  return (
    <svg className={`animate-spin ${className}`} xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  )
}

// Best-effort BR phone formatting for the number picker.
function formatPhone(raw: string): string {
  if (!raw) return 'Número desconhecido'
  const d = raw.replace(/\D/g, '')
  if (d.length >= 12 && d.startsWith('55')) {
    const ddd = d.slice(2, 4)
    const rest = d.slice(4)
    return `+55 (${ddd}) ${rest.slice(0, rest.length - 4)}-${rest.slice(-4)}`
  }
  return `+${d}`
}

export default function CampaignModal({
  searchId,
  searchLabel,
  onClose,
}: {
  searchId: string
  searchLabel?: string
  onClose: () => void
}) {
  const [sessions, setSessions] = useState<WhatsAppSession[]>([])
  const [loadingSessions, setLoadingSessions] = useState(true)
  const [selectedSessionId, setSelectedSessionId] = useState('')
  const [message, setMessage] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [campaign, setCampaign] = useState<Campaign | null>(null)

  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // Load connected numbers once when the modal opens.
  useEffect(() => {
    let active = true
    ;(async () => {
      try {
        const res = await fetch(`${API_URL}/api/v1/whatsapp/sessions`)
        if (!res.ok) throw new Error(`Erro ${res.status}`)
        const data: WhatsAppSession[] = await res.json()
        if (!active) return
        const connected = (data ?? []).filter((s) => s.status === 'CONNECTED')
        setSessions(connected)
        if (connected.length > 0) setSelectedSessionId(connected[0].id)
      } catch {
        if (active) setError('Não foi possível carregar os números de WhatsApp conectados.')
      } finally {
        if (active) setLoadingSessions(false)
      }
    })()
    return () => {
      active = false
    }
  }, [])

  // Poll campaign progress while it is running.
  useEffect(() => {
    if (!campaign || campaign.status === 'COMPLETED') return
    pollRef.current = setInterval(async () => {
      try {
        const res = await fetch(`${API_URL}/api/v1/campaigns/${campaign.id}`)
        if (!res.ok) return
        const data: Campaign = await res.json()
        setCampaign(data)
      } catch {
        // transient; keep polling
      }
    }, POLL_MS)
    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
    }
  }, [campaign])

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault()
      setSubmitting(true)
      setError(null)
      try {
        const res = await fetch(`${API_URL}/api/v1/campaigns`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            search_id: searchId,
            whatsapp_session_id: selectedSessionId,
            message,
          }),
        })
        const body = await res.json().catch(() => null)
        if (!res.ok) {
          throw new Error(body?.error || `Erro ${res.status}`)
        }
        setCampaign(body as Campaign)
      } catch (err) {
        if (err instanceof TypeError) {
          setError('Não foi possível conectar à API em ' + API_URL)
        } else {
          setError(err instanceof Error ? err.message : 'Falha ao iniciar a campanha.')
        }
      } finally {
        setSubmitting(false)
      }
    },
    [searchId, selectedSessionId, message],
  )

  const noNumbers = !loadingSessions && sessions.length === 0
  const attempted = campaign ? campaign.sent + campaign.failed : 0
  const pct = campaign && campaign.total > 0 ? Math.round((attempted / campaign.total) * 100) : 0

  return (
    <div
      className="fixed inset-0 z-30 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
      aria-modal="true"
      onClick={onClose}
    >
      <div className="bg-white rounded-2xl shadow-xl w-full max-w-lg p-6 relative" onClick={(e) => e.stopPropagation()}>
        <button onClick={onClose} aria-label="Fechar" className="absolute top-4 right-4 text-gray-400 hover:text-gray-600">
          <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>

        <h2 className="text-lg font-semibold text-gray-900 mb-1">Iniciar Campanha</h2>
        <p className="text-sm text-gray-500 mb-5">
          {searchLabel ? <>Disparo para os leads de <strong>{searchLabel}</strong>.</> : 'Disparo para os leads desta busca.'}
        </p>

        {/* --- Running / progress view --- */}
        {campaign ? (
          <div className="flex flex-col gap-4">
            <div>
              <div className="flex items-center justify-between mb-1.5">
                <span className="text-sm font-medium text-gray-700">
                  {campaign.status === 'COMPLETED' ? 'Disparo concluído' : 'Disparando…'}
                </span>
                <span className="text-sm text-gray-500" data-testid="campaign-progress">
                  {campaign.progress}
                </span>
              </div>
              <div className="h-3 w-full rounded-full bg-gray-100 overflow-hidden">
                <div
                  className={`h-full rounded-full transition-all duration-500 ${
                    campaign.status === 'COMPLETED' ? 'bg-green-500' : 'bg-blue-600'
                  }`}
                  style={{ width: `${pct}%` }}
                  role="progressbar"
                  aria-valuenow={pct}
                  aria-valuemin={0}
                  aria-valuemax={100}
                />
              </div>
              <div className="flex justify-between mt-2 text-xs text-gray-500">
                <span>{attempted} de {campaign.total} processados</span>
                <span>
                  <span className="text-green-600 font-medium">{campaign.sent} enviados</span>
                  {campaign.failed > 0 && (
                    <> · <span className="text-red-500 font-medium">{campaign.failed} falharam</span></>
                  )}
                </span>
              </div>
            </div>

            {campaign.status !== 'COMPLETED' ? (
              <div className="flex items-center gap-2 text-sm text-gray-500">
                <Spinner />
                O envio acontece em segundo plano (intervalo anti-ban de 30–90s). Você pode fechar esta janela.
              </div>
            ) : (
              <div className="rounded-lg bg-green-50 border border-green-200 text-green-700 text-sm px-4 py-3">
                Campanha finalizada: {campaign.sent} mensagem(ns) enviada(s)
                {campaign.failed > 0 && `, ${campaign.failed} não entregue(s)`}.
              </div>
            )}

            <button
              onClick={onClose}
              className="self-end px-5 py-2 rounded-lg bg-gray-100 hover:bg-gray-200 text-gray-700 text-sm font-medium transition-colors"
            >
              Fechar
            </button>
          </div>
        ) : (
          /* --- Compose view --- */
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            {/* Number picker */}
            <div>
              <label htmlFor="wa-number" className="block text-sm font-medium text-gray-700 mb-1.5">
                Número de WhatsApp
              </label>
              {loadingSessions ? (
                <div className="flex items-center gap-2 text-sm text-gray-400 py-2">
                  <Spinner /> Carregando números…
                </div>
              ) : noNumbers ? (
                <p className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded-lg px-3 py-2">
                  Nenhum número conectado. Conecte um número na aba <strong>WhatsApp</strong> antes de disparar.
                </p>
              ) : (
                <select
                  id="wa-number"
                  value={selectedSessionId}
                  onChange={(e) => setSelectedSessionId(e.target.value)}
                  className="w-full px-4 py-2.5 rounded-lg border border-gray-200 bg-gray-50 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 text-gray-900 text-sm"
                >
                  {sessions.map((s) => (
                    <option key={s.id} value={s.id}>
                      {formatPhone(s.phone_number)}
                    </option>
                  ))}
                </select>
              )}
            </div>

            {/* Message */}
            <div>
              <label htmlFor="wa-message" className="block text-sm font-medium text-gray-700 mb-1.5">
                Mensagem
              </label>
              <textarea
                id="wa-message"
                rows={4}
                maxLength={MAX_MESSAGE}
                placeholder="Olá! Temos uma oferta especial para a sua empresa…"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                className="w-full px-4 py-2.5 rounded-lg border border-gray-200 bg-gray-50 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 text-gray-900 placeholder-gray-400 text-sm resize-none"
              />
              <p className="text-xs text-gray-400 mt-1 text-right">{message.length}/{MAX_MESSAGE}</p>
            </div>

            {/* Anti-ban notice */}
            <div className="flex items-start gap-2.5 rounded-lg bg-amber-50 border border-amber-200 px-4 py-3">
              <svg className="h-5 w-5 text-amber-500 mt-0.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
                <path
                  fillRule="evenodd"
                  d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z"
                  clipRule="evenodd"
                />
              </svg>
              <p className="text-xs text-amber-800 leading-relaxed">
                <strong>Disparo inteligente (anti-ban):</strong> as mensagens são enviadas com intervalo aleatório de
                30 a 90 segundos, com limite por hora por número e validação de cada destino no WhatsApp. O envio
                continua em segundo plano mesmo se você fechar esta janela.
              </p>
            </div>

            {error && <p className="text-sm text-red-600">{error}</p>}

            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                className="px-5 py-2 rounded-lg bg-gray-100 hover:bg-gray-200 text-gray-700 text-sm font-medium transition-colors"
              >
                Cancelar
              </button>
              <button
                type="submit"
                disabled={submitting || noNumbers || !selectedSessionId || message.trim() === ''}
                className="inline-flex items-center gap-2 px-5 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 disabled:bg-blue-300 disabled:cursor-not-allowed text-white text-sm font-semibold transition-colors"
              >
                {submitting && <Spinner />}
                Iniciar Disparo
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}
