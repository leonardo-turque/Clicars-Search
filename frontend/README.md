# Clicars Search — Frontend

Interface web do sistema de busca de empresas, construída com **Next.js 14** (App Router), **TypeScript** e **TailwindCSS 3**.

## Arquitetura

```
src/
└── app/
    ├── layout.tsx   # Root layout com metadados e fonte global
    ├── page.tsx     # Dashboard (formulário + tabela de resultados)
    └── globals.css  # Diretivas Tailwind e import da fonte Inter
```

O dashboard é um único componente cliente (`'use client'`) que:
1. Recebe os inputs do usuário (nicho, localização, quantidade).
2. Faz um `POST /api/v1/searches` para o backend Go.
3. Exibe o ID de execução e a tabela de empresas retornada.

A URL da API é configurada via variável de ambiente `NEXT_PUBLIC_API_URL` (padrão: `http://localhost:8080`).

## Desenvolvimento local

```bash
# Instalar dependências
npm install

# Iniciar servidor de desenvolvimento (porta 3000)
npm run dev

# Build de produção
npm run build
npm start
```

## Docker

```bash
# Build da imagem (na raiz do projeto)
docker build -t clicars-frontend ./frontend

# Rodar isoladamente
docker run -p 3000:3000 -e NEXT_PUBLIC_API_URL=http://localhost:8080 clicars-frontend

# Subir todo o stack (db + api + frontend)
docker compose up --build
```

O `Dockerfile` usa **multi-stage build**:
- `deps` — instala as dependências npm.
- `builder` — executa `next build` gerando o output standalone.
- `runner` — imagem mínima `node:20-alpine` que serve `server.js`.
