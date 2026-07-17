import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import Dashboard from './page'

describe('Dashboard (search form)', () => {
  const originalFetch = global.fetch

  afterEach(() => {
    global.fetch = originalFetch
    jest.restoreAllMocks()
  })

  it('renders the search form with its fields and the submit button', () => {
    render(<Dashboard />)

    expect(screen.getByRole('heading', { name: /clicars search/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/nicho/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/cidade/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/quantidade/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /buscar empresas/i })).toBeInTheDocument()
  })

  it('shows the loading state after the search is submitted', async () => {
    // jsdom has no fetch, so install a stub that never resolves: the component
    // stays in its "loading" state, which is exactly what we want to assert.
    const fetchMock = jest.fn(() => new Promise<never>(() => {}))
    global.fetch = fetchMock as unknown as typeof fetch

    const user = userEvent.setup()
    render(<Dashboard />)

    await user.type(screen.getByLabelText(/nicho/i), 'Padarias')
    await user.type(screen.getByLabelText(/cidade/i), 'São Paulo')
    await user.click(screen.getByRole('button', { name: /buscar empresas/i }))

    // While loading, the button label switches to "Buscando..." and is disabled.
    const loadingButton = await screen.findByRole('button', { name: /buscando/i })
    expect(loadingButton).toBeDisabled()
    // 2 calls: one for the history load on mount, one for the search submit.
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('opens the campaign dispatch panel for a search and lists connected numbers', async () => {
    const search = {
      search_id: 'srch-1',
      niche: 'Padarias',
      location: 'São Paulo',
      quantity: 10,
      created_at: '2026-06-15T12:00:00Z',
      companies: [
        {
          id: 'c1',
          search_id: 'srch-1',
          name: 'Pão Quente',
          location: 'São Paulo',
          phone: '11999990000',
          website: '',
          created_at: '2026-06-15T12:00:00Z',
        },
      ],
    }
    const sessions = [{ id: 'sess-1', phone_number: '5511988887777', status: 'CONNECTED' }]

    // Route by URL + method: history (GET), search submit (POST), WhatsApp sessions (GET).
    const fetchMock = jest.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      const reply = (body: unknown) =>
        Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(body) } as Response)

      if (url.endsWith('/api/v1/searches') && method === 'POST') return reply(search)
      if (url.endsWith('/api/v1/searches')) return reply([])
      if (url.endsWith('/api/v1/whatsapp/sessions')) return reply(sessions)
      return Promise.reject(new Error(`unexpected fetch: ${method} ${url}`))
    })
    global.fetch = fetchMock as unknown as typeof fetch

    const user = userEvent.setup()
    render(<Dashboard />)

    await user.type(screen.getByLabelText(/nicho/i), 'Padarias')
    await user.type(screen.getByLabelText(/cidade/i), 'São Paulo')
    await user.click(screen.getByRole('button', { name: /buscar empresas/i }))

    // Results render; the "Iniciar Campanha" CTA opens the dispatch panel.
    const startBtn = await screen.findByRole('button', { name: /iniciar campanha/i })
    await user.click(startBtn)

    // The panel shows the anti-ban notice and offers the connected number.
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(await screen.findByText(/disparo inteligente \(anti-ban\)/i)).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByRole('option', { name: /98888-7777/ })).toBeInTheDocument(),
    )
  })
})
