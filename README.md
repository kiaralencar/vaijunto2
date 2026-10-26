# VaiJunto — Consórcio de Caronas Compartilhadas

Problema 2 da disciplina TEC502 – MI Concorrência e Conectividade (UEFS, 2026.2).

Evolução do VaiJunto do [Problema 1](https://github.com/kiaralencar/vaijunto)
(um servidor central) para um **consórcio de empresas de caronas**, cada uma
com o seu próprio servidor, as suas caronas e o seu armazenamento. Um
passageiro de qualquer empresa busca e reserva itinerários que combinam
trechos de caronas de várias empresas, sem conta nas outras, e sem nenhum
servidor central.

## Arquitetura

```
  motorista / passageiro            motorista / passageiro
          │ TCP (protocolo do P1)            │
          ▼                                  ▼
  ┌───────────────┐   API REST (HTTP)  ┌───────────────┐
  │   vaijunto    │◄──────────────────►│    rotasul    │
  │ :8080  :9090  │                    │ :8080  :9090  │
  └───────┬───────┘                    └───────┬───────┘
          │            API REST (HTTP)         │
          └──────────────►┌───────────────┐◄───┘
                          │ caminhonorte  │
                          │ :8080  :9090  │
                          └───────────────┘
```

- **Porta TCP (8080)**: clientes motorista e passageiro, reaproveitados do
  Problema 1. Cada cliente conecta **só** no servidor da sua empresa.
- **Porta HTTP (9090)**: API REST usada **só entre servidores**
  ([especificação completa](docs/api-rest.md)).
- **Vizinhos configuráveis**: cada servidor conhece os endereços das empresas
  parceiras pela flag `-vizinhos`. Com 3 empresas, cada uma lista as outras
  duas (malha completa).

Principais mecanismos (todos implementados no próprio servidor):

| Problema do enunciado | Solução |
|---|---|
| Itinerário atômico entre empresas | *Two-phase commit* próprio: o servidor do passageiro coordena; cada empresa dona de trecho prepara (segura o assento) e depois confirma ou aborta |
| Mesmo assento nunca confirmado duas vezes | Só a empresa dona da carona altera os seus assentos, sob o mutex dela |
| Precedência na disputa do último assento | Timestamp de **Lamport** por reserva; menor vence, empate pelo ID da empresa. Reserva com precedência pode tomar o assento de uma mais nova **ainda não confirmada**; a mais nova nunca espera (sem deadlock) |
| Queda de servidor no meio da reserva | Gravação em disco antes de cada resposta, prazo para assentos segurados, consulta da decisão ao coordenador, aborto das reservas sem decisão ao reiniciar, reenvio de mensagens até a entrega |
| Detecção de queda | Timeout de 2 s em toda requisição entre servidores e verificação periódica de `GET /status` (heartbeat) |

## Estrutura

```
cmd/servidor/          servidor de uma empresa
  main.go              porta TCP dos clientes, inicialização
  config.go            flags
  handlers.go          operações do protocolo dos clientes
  api.go               API REST entre servidores
  servidor.go          coordenação das reservas (coordenador e participante)
  estado.go            estado da empresa, protegido por mutex
  busca.go             busca distribuída (DFS sobre as caronas do consórcio)
  vizinhos.go          vizinhos, heartbeat e requisições HTTP
  lamport.go           relógio lógico de Lamport e critério de precedência
  persistencia.go      gravação atômica em disco
  *_test.go            testes (ver abaixo)
cmd/cliente-motorista  cliente do motorista (do Problema 1)
cmd/cliente-passageiro cliente do passageiro (do Problema 1)
cmd/simulador          simulador de passageiros, sem interface
internal/dominio       entidades: carona, reserva, participação
internal/protocolo     mensagens do protocolo dos clientes
internal/cliente       cliente TCP mínimo usado pelo simulador e testes
docs/                  protocolo, API REST, diagramas, referências
```

## Como executar

Requer Go 1.23+ (ou só Docker).

### Na mesma máquina, com `go run` (3 terminais)

```
go run ./cmd/servidor -empresa=vaijunto     -porta-tcp=8080 -porta-http=9090 -vizinhos=http://localhost:9091,http://localhost:9092
go run ./cmd/servidor -empresa=rotasul      -porta-tcp=8081 -porta-http=9091 -vizinhos=http://localhost:9090,http://localhost:9092
go run ./cmd/servidor -empresa=caminhonorte -porta-tcp=8082 -porta-http=9092 -vizinhos=http://localhost:9090,http://localhost:9091
```

Clientes (cada um conecta no servidor da sua empresa):
```
go run ./cmd/cliente-motorista  localhost:8081
go run ./cmd/cliente-passageiro localhost:8080
```

Flags do servidor (`-h` mostra todas):

| Flag | Padrão | Significado |
|---|---|---|
| `-empresa` | (obrigatória) | ID da empresa no consórcio |
| `-porta-tcp` | `8080` | Porta dos clientes |
| `-porta-http` | `9090` | Porta da API REST |
| `-vizinhos` | (nenhum) | URLs dos vizinhos, separadas por vírgula |
| `-dados` | `dados` | Pasta de dados (subpasta por empresa); vazio desliga a gravação |
| `-prazo-decisao` | `10s` | Tempo que um assento fica segurado esperando a decisão |
| `-atraso-decisao` | `0` | Atraso entre as fases da reserva (só para testes de falha) |

### Com Docker Compose (mesma máquina)

```
docker compose up --build vaijunto rotasul caminhonorte
docker compose run --rm cliente-passageiro vaijunto:8080
docker compose run --rm cliente-motorista rotasul:8080
docker compose run --rm simulador
```

Os dados de cada empresa ficam num volume próprio, então
`docker compose restart rotasul` (ou `docker compose kill rotasul` seguido de
`docker compose start rotasul`) simula queda e recuperação.

### No laboratório (cada empresa num computador)

Em cada máquina, descobrir o IP (`hostname -I`) e subir o servidor listando
os IPs das outras duas:

```
docker build -f docker/servidor.Dockerfile -t vaijunto-servidor .

# máquina 1 (IP1)
docker run -p 8080:8080 -p 9090:9090 -v dados:/dados vaijunto-servidor -empresa=vaijunto -vizinhos=http://IP2:9090,http://IP3:9090
# máquina 2 (IP2)
docker run -p 8080:8080 -p 9090:9090 -v dados:/dados vaijunto-servidor -empresa=rotasul -vizinhos=http://IP1:9090,http://IP3:9090
# máquina 3 (IP3)
docker run -p 8080:8080 -p 9090:9090 -v dados:/dados vaijunto-servidor -empresa=caminhonorte -vizinhos=http://IP1:9090,http://IP2:9090
```

Para derrubar uma empresa: `docker kill <id>` (queda abrupta) ou
`docker pause <id>` (servidor travado, sem responder: os outros detectam por
timeout). Para religar com o mesmo estado: `docker start <id>` /
`docker unpause <id>`.

## Simulador de passageiros

Gera, sem interface, buscas, reservas e cancelamentos simultâneos em todas as
empresas. A cada 2 s (flag `-progresso`) mostra uma linha com o andamento
(buscas, reservas confirmadas e recusadas, cancelamentos, quedas de conexão);
no final, mostra o total de cada resultado, a vazão e os tempos de resposta
(média, p50, p95, máximo).

```
go run ./cmd/simulador -servidores=localhost:8080,localhost:8081,localhost:8082 -passageiros=100 -duracao=30s -semear
```

`-semear` publica antes caronas em cadeia (Salvador → Feira de Santana na
primeira empresa, Feira → Vitória da Conquista na segunda, Conquista → Montes
Claros na terceira), para existirem itinerários que só se completam
combinando as três. `-h` mostra as demais opções.

## Testes automatizados

```
go test ./... -v
```

| Teste | O que verifica |
|---|---|
| `TestConsorcioCargaComQuedaDeServidor` | 3 empresas como **processos reais**; 60 passageiros disputando itinerários que cruzam as três; uma empresa é **derrubada com kill** no meio da carga e religada. Ao final confere, em todas as empresas: nenhum assento confirmado duas vezes, nenhum itinerário confirmado pela metade, nenhum assento travado |
| `TestQuedaDoCoordenadorNoMeioDaReserva` | Coordenador cai depois de o participante segurar o assento; o participante não decide sozinho; ao voltar, o coordenador aborta e o assento é liberado |
| `TestQuedaDoParticipanteDepoisDePreparar` | Participante cai depois de aceitar; a reserva é confirmada e o participante recebe a confirmação ao voltar |
| `TestCriterioDePrecedencia`, `TestDisputaPeloUltimoAssento` | Ordem de Lamport com desempate; reserva com precedência toma o assento de uma mais nova não confirmada; assento confirmado nunca é tomado |
| `TestPreparacaoAtrasadaDepoisDoAborto` | Mensagem de preparação que chega depois do aborto é recusada |

O detector de condições de corrida do Go (`go test -race ./...`) precisa de
um compilador C de 64 bits; no Docker:
```
docker run --rm -v "${PWD}:/src" -w /src golang:1.23 go test -race ./...
```

## Documentação

- [`docs/api-rest.md`](docs/api-rest.md) — API REST entre servidores: recursos, métodos, formatos, códigos de retorno e diagramas de sequência
- [`docs/protocolo.md`](docs/protocolo.md) — protocolo TCP dos clientes
- [`docs/referencias.txt`](docs/referencias.txt) — referências
