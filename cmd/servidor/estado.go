package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vaijunto/internal/dominio"
)

// Erros devolvidos pelas operações de reserva, usados para escolher o código HTTP
var (
	errSemAssento    = errors.New("sem assento livre")
	errInvalido      = errors.New("pedido invalido")
	errNaoEncontrada = errors.New("nao encontrada")
	errConflito      = errors.New("estado incompativel")
)

// dataParaComparar converte "DD/MM/AAAA" num tempo comparável.
func dataParaComparar(data string) time.Time {
	t, err := time.Parse("02/01/2006", data)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Mensagem é uma notificação a uma empresa que ainda não foi entregue:
// a decisão sobre uma reserva (confirmar, abortar, cancelar), ou o aviso ao
// coordenador de que uma carona da reserva foi cancelada. As mensagens não são
// gravadas à parte: são calculadas a partir das reservas e participações, então
// sobrevivem a uma queda junto com o registro que as originou.
type Mensagem struct {
	Reserva     string
	Empresa     string
	Caminho     string
	Invalidacao bool
}

// meta guarda o que não pertence a nenhum registro específico. Os contadores
// de ID e o relógio não ficam aqui: são recalculados a partir dos registros.
type meta struct {
	Senhas map[string]string
}

// Estado é todo o estado desta empresa: as suas caronas, as reservas dos seus
// passageiros e as participações nas reservas (próprias ou de outras empresas).
// Um único mutex protege tudo. Cada alteração grava um único arquivo, para que
// uma queda no meio nunca deixe dois registros contraditórios.
type Estado struct {
	mu                  sync.Mutex
	empresa             string
	arm                 *Armazenamento
	relogio             *Relogio
	proximoCarona       int
	proximaReserva      int
	caronas             map[string]*dominio.Carona
	reservas            map[string]*dominio.Reserva
	participacoes       map[string]*dominio.Participacao
	invalidacoesAceitas map[string]bool // Avisos de carona cancelada já recebidos pelo coordenador
	senhas              map[string]string
}

// Inicializa o estado da empresa, com mapas vazios e contadores zerados
func NovoEstado(empresa string, arm *Armazenamento, relogio *Relogio) *Estado {
	return &Estado{
		empresa:             empresa,
		arm:                 arm,
		relogio:             relogio,
		caronas:             make(map[string]*dominio.Carona),
		reservas:            make(map[string]*dominio.Reserva),
		participacoes:       make(map[string]*dominio.Participacao),
		invalidacoesAceitas: make(map[string]bool),
		senhas:              make(map[string]string),
	}
}

// Carrega do disco tudo o que a empresa tinha gravado antes de cair
func (e *Estado) Carrega() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var m meta
	if _, err := e.arm.LeMeta(&m); err != nil {
		return err
	}
	if m.Senhas != nil {
		e.senhas = m.Senhas
	}

	// Os contadores continuam do maior ID já gravado, e o relógio do maior timestamp
	err := e.arm.LeTodos(tipoCarona, func(dados []byte) error {
		var c dominio.Carona
		if err := json.Unmarshal(dados, &c); err != nil {
			return err
		}
		e.caronas[c.ID] = &c
		e.proximoCarona = max(e.proximoCarona, numeroDoID(c.ID, e.empresa+"-C"))
		return nil
	})
	if err == nil {
		err = e.arm.LeTodos(tipoReserva, func(dados []byte) error {
			var r dominio.Reserva
			if err := json.Unmarshal(dados, &r); err != nil {
				return err
			}
			e.reservas[r.ID] = &r
			e.proximaReserva = max(e.proximaReserva, numeroDoID(r.ID, e.empresa+"-R"))
			e.relogio.Define(r.Relogio)
			return nil
		})
	}
	if err == nil {
		err = e.arm.LeTodos(tipoParticipacao, func(dados []byte) error {
			var p dominio.Participacao
			if err := json.Unmarshal(dados, &p); err != nil {
				return err
			}
			e.participacoes[p.ID] = &p
			e.relogio.Define(p.Relogio)
			return nil
		})
	}
	if err != nil {
		return err
	}

	// Assentos livres não são gravados: são recalculados a partir das participações
	for _, c := range e.caronas {
		c.AssentosLivres = append([]int(nil), c.AssentosTotais...)
	}
	for _, p := range e.participacoes {
		if p.Estado == dominio.Preparada || p.Estado == dominio.Confirmada {
			e.ocupaAssentos(p, -1)
		}
	}
	return nil
}

// Grava as senhas
func (e *Estado) gravaMeta() {
	e.arm.GravaMeta(meta{Senhas: e.senhas})
}

// Extrai o número de um ID no formato "<prefixo><número>" (0 se não for desta empresa)
func numeroDoID(id, prefixo string) int {
	if !strings.HasPrefix(id, prefixo) {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(id, prefixo))
	return n
}

// Autentica resolve o problema de dois clientes usarem o mesmo nome
func (e *Estado) Autentica(nome, senha string) error {
	e.mu.Lock()         // Trava o mutex para proteger o acesso ao mapa de senhas
	defer e.mu.Unlock() // Destrava o mutex ao sair da função

	// Se o nome não existe, cria a senha.
	// Se existe, compara com a senha fornecida.
	senhaExistente, jaExiste := e.senhas[nome]
	if !jaExiste {
		e.senhas[nome] = senha
		e.gravaMeta()
		return nil
	}
	if senhaExistente != senha {
		return fmt.Errorf("Nome ja esta em uso com outra senha")
	}
	return nil
}

// Criacao de uma nova carona com ID único no consórcio ("<empresa>-C<n>"), e adicao ao mapa de caronas
func (e *Estado) PublicarCarona(motorista string, rota []string, data string, preco []float64, assentos []int) dominio.Carona {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.proximoCarona++
	id := fmt.Sprintf("%s-C%d", e.empresa, e.proximoCarona)

	c := &dominio.Carona{
		ID:             id,
		Motorista:      motorista,
		Rota:           rota,
		Data:           data,
		PrecoPorTrecho: preco,
		AssentosTotais: append([]int(nil), assentos...),
		AssentosLivres: append([]int(nil), assentos...),
		Ativa:          true,
	}
	e.caronas[id] = c
	e.arm.Grava(tipoCarona, id, c)
	return *c
}

// ListarCaronas devolve uma cópia das caronas ativas, ordenadas por data e ID
func (e *Estado) ListarCaronas() []dominio.Carona {
	e.mu.Lock()
	defer e.mu.Unlock()

	lista := make([]dominio.Carona, 0, len(e.caronas))
	for _, c := range e.caronas {
		if c.Ativa {
			copia := *c
			copia.AssentosLivres = append([]int(nil), c.AssentosLivres...) // Copia também os assentos
			lista = append(lista, copia)
		}
	}
	ordenaCaronas(lista)
	return lista
}

// Ordenação: data mais próxima primeiro. Desempate pelo ID.
func ordenaCaronas(lista []dominio.Carona) {
	sort.Slice(lista, func(i, j int) bool {
		if lista[i].Data != lista[j].Data {
			return dataParaComparar(lista[i].Data).Before(dataParaComparar(lista[j].Data))
		}
		return lista[i].ID < lista[j].ID
	})
}

// CancelarCarona cancela uma carona, tornando-a inativa.
// Só o motorista dono da carona pode cancelar. As reservas que usavam a
// carona passam a gerar avisos aos seus coordenadores (ver mensagensPendentes).
func (e *Estado) CancelarCarona(motorista, caronaID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, existe := e.caronas[caronaID]
	if !existe {
		return fmt.Errorf("Carona nao encontrada")
	}
	if c.Motorista != motorista {
		return fmt.Errorf("Carona pertence a outro motorista")
	}
	c.Ativa = false // Não apaga do map, só desativa
	e.arm.Grava(tipoCarona, c.ID, c)
	return nil
}

// Devolve os passageiros confirmados de uma carona para o motorista dela, ou
// seja, um motorista não pode ver quem reservou a carona alheia
func (e *Estado) ConsultarPassageiros(solicitante, caronaID string) ([]dominio.PassageiroNoTrecho, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, existe := e.caronas[caronaID]
	if !existe {
		return nil, fmt.Errorf("Carona nao encontrada")
	}
	if c.Motorista != solicitante {
		return nil, fmt.Errorf("Carona pertence a outro motorista")
	}

	var passageiros []dominio.PassageiroNoTrecho
	for _, p := range e.participacoes {
		if p.Estado != dominio.Confirmada {
			continue
		}
		nome := p.Passageiro
		if p.Coordenador != e.empresa {
			nome = fmt.Sprintf("%s (%s)", p.Passageiro, p.Coordenador)
		}
		for _, item := range p.Itens {
			if item.CaronaID == caronaID {
				passageiros = append(passageiros, dominio.PassageiroNoTrecho{
					Passageiro:   nome,
					TrechoInicio: item.TrechoInicio,
					TrechoFim:    item.TrechoFim,
				})
			}
		}
	}
	return passageiros, nil
}

// ---------- Papel de participante (dono dos trechos) ----------

// PreparaParticipacao segura os assentos pedidos por uma reserva, se houver
// assento em todos os trechos (tudo ou nada dentro desta empresa). Se faltar
// assento, devolve também as participações ainda não confirmadas, mais novas
// que esta pelo critério de Lamport, que ocupam os trechos em falta
// (candidatas a ceder o lugar).
func (e *Estado) PreparaParticipacao(p dominio.Participacao, prazo time.Duration) ([]dominio.ItemReserva, []dominio.Participacao, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Pedido repetido (reenvio do coordenador): responde igual à primeira vez
	if atual, existe := e.participacoes[p.ID]; existe {
		if atual.Estado == dominio.Preparada || atual.Estado == dominio.Confirmada {
			return copiaItens(atual.Itens), nil, nil
		}
		return nil, nil, fmt.Errorf("%w: reserva %s ja foi %s", errConflito, p.ID, atual.Estado)
	}
	if len(p.Itens) == 0 {
		return nil, nil, fmt.Errorf("%w: nenhum item", errInvalido)
	}

	// Primeira passada: validação, e quanto cada trecho precisa
	type trecho struct {
		carona string
		t      int
	}
	precisa := make(map[trecho]int)
	itens := make([]dominio.ItemReserva, len(p.Itens))
	for i, item := range p.Itens {
		c, existe := e.caronas[item.CaronaID]
		if !existe || !c.Ativa {
			return nil, nil, fmt.Errorf("%w: carona %s nao encontrada ou inativa", errInvalido, item.CaronaID)
		}
		if item.TrechoInicio < 0 || item.TrechoFim >= c.NumTrechos() || item.TrechoInicio > item.TrechoFim {
			return nil, nil, fmt.Errorf("%w: intervalo de trechos invalido para carona %s", errInvalido, item.CaronaID)
		}
		item.Empresa = e.empresa
		item.Origem = c.Rota[item.TrechoInicio]
		item.Destino = c.Rota[item.TrechoFim+1]
		item.Preco = 0
		for t := item.TrechoInicio; t <= item.TrechoFim; t++ {
			item.Preco += c.PrecoPorTrecho[t]
			precisa[trecho{c.ID, t}]++
		}
		itens[i] = item
	}

	emFalta := make(map[trecho]bool)
	for tr, n := range precisa {
		if e.caronas[tr.carona].AssentosLivres[tr.t] < n {
			emFalta[tr] = true
		}
	}
	if len(emFalta) > 0 {
		var candidatas []dominio.Participacao
		for _, q := range e.participacoes {
			if q.Estado != dominio.Preparada || !temPrecedencia(p.Relogio, p.Coordenador, q.Relogio, q.Coordenador) {
				continue
			}
			for _, item := range q.Itens {
				ocupa := false
				for t := item.TrechoInicio; t <= item.TrechoFim; t++ {
					if emFalta[trecho{item.CaronaID, t}] {
						ocupa = true
					}
				}
				if ocupa {
					candidatas = append(candidatas, copiaParticipacao(q))
					break
				}
			}
		}
		return nil, candidatas, fmt.Errorf("%w em algum trecho da empresa %s", errSemAssento, e.empresa)
	}

	// Segunda passada: aplicação. Só a participação é gravada; os assentos
	// livres são recalculados a partir dela se a empresa cair.
	nova := p
	nova.Itens = itens
	nova.Estado = dominio.Preparada
	nova.Expira = time.Now().Add(prazo)
	e.participacoes[p.ID] = &nova
	e.ocupaAssentos(&nova, -1)
	e.arm.Grava(tipoParticipacao, p.ID, &nova)
	return copiaItens(itens), nil, nil
}

// Torna definitiva uma participação preparada
func (e *Estado) ConfirmaParticipacao(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	p, existe := e.participacoes[id]
	if !existe {
		return errNaoEncontrada
	}
	switch p.Estado {
	case dominio.Confirmada:
		return nil
	case dominio.Preparada:
		p.Estado = dominio.Confirmada
		e.arm.Grava(tipoParticipacao, id, p)
		return nil
	}
	return fmt.Errorf("%w: reserva %s ja foi %s", errConflito, id, p.Estado)
}

// Desfaz uma participação preparada, devolvendo os assentos. Se a participação
// ainda não existir, registra a reserva como abortada, para recusar um pedido
// de preparação que chegue atrasado.
func (e *Estado) AbortaParticipacao(id string) error {
	return e.encerraParticipacao(id, dominio.Abortada)
}

// Cancela uma participação confirmada (passageiro desistiu ou carona cancelada)
func (e *Estado) CancelaParticipacao(id string) error {
	return e.encerraParticipacao(id, dominio.Cancelada)
}

func (e *Estado) encerraParticipacao(id, novoEstado string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	p, existe := e.participacoes[id]
	if !existe {
		p = &dominio.Participacao{ID: id, Estado: novoEstado}
		e.participacoes[id] = p
		e.arm.Grava(tipoParticipacao, id, p)
		return nil
	}
	switch p.Estado {
	case dominio.Abortada, dominio.Cancelada:
		return nil
	case dominio.Confirmada:
		if novoEstado == dominio.Abortada {
			return fmt.Errorf("%w: reserva %s ja foi confirmada", errConflito, id)
		}
	}
	e.ocupaAssentos(p, +1)
	p.Estado = novoEstado
	e.arm.Grava(tipoParticipacao, id, p)
	return nil
}

// Soma "delta" aos assentos livres dos trechos de uma participação
// (-1 ao segurar, +1 ao devolver)
func (e *Estado) ocupaAssentos(p *dominio.Participacao, delta int) {
	for _, item := range p.Itens {
		c, existe := e.caronas[item.CaronaID]
		if !existe {
			continue
		}
		for t := item.TrechoInicio; t <= item.TrechoFim; t++ {
			c.AssentosLivres[t] += delta
		}
	}
}

// Devolve as participações preparadas cujo prazo de decisão já venceu
func (e *Estado) ParticipacoesVencidas(agora time.Time) []dominio.Participacao {
	e.mu.Lock()
	defer e.mu.Unlock()

	var lista []dominio.Participacao
	for _, p := range e.participacoes {
		if p.Estado == dominio.Preparada && agora.After(p.Expira) {
			lista = append(lista, copiaParticipacao(p))
		}
	}
	return lista
}

// Adia o prazo de uma participação preparada (o coordenador ainda não decidiu)
func (e *Estado) AdiaParticipacao(id string, prazo time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if p, existe := e.participacoes[id]; existe && p.Estado == dominio.Preparada {
		p.Expira = time.Now().Add(prazo)
		e.arm.Grava(tipoParticipacao, id, p)
	}
}

// ---------- Papel de coordenador (empresa do passageiro) ----------

// Cria a reserva no estado "preparando", com ID único no consórcio ("<empresa>-R<n>")
func (e *Estado) CriaReserva(passageiro string, itens []dominio.ItemReserva, relogio int64) dominio.Reserva {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.proximaReserva++
	r := &dominio.Reserva{
		ID:         fmt.Sprintf("%s-R%d", e.empresa, e.proximaReserva),
		Passageiro: passageiro,
		Itens:      copiaItens(itens),
		Relogio:    relogio,
		Estado:     dominio.Preparando,
	}
	e.reservas[r.ID] = r
	e.arm.Grava(tipoReserva, r.ID, r)
	return copiaReserva(r)
}

// Registra a decisão final da reserva, se ainda estiver preparando. Se outra
// reserva com precedência já a tiver abortado, a decisão anterior prevalece.
func (e *Estado) DecideReserva(id string, confirmar bool, itens []dominio.ItemReserva) dominio.Reserva {
	e.mu.Lock()
	defer e.mu.Unlock()

	r := e.reservas[id]
	if r.Estado == dominio.Preparando {
		if confirmar {
			if itens != nil {
				r.Itens = copiaItens(itens)
			}
			e.mudaReserva(r, dominio.Confirmada)
		} else {
			e.mudaReserva(r, dominio.Abortada)
		}
	}
	return copiaReserva(r)
}

// Pedido de outra reserva com precedência: aborta esta, se ainda não foi decidida
func (e *Estado) Preempcao(id string) (dominio.Reserva, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, existe := e.reservas[id]
	if !existe {
		return dominio.Reserva{}, errNaoEncontrada
	}
	if r.Estado == dominio.Preparando {
		e.mudaReserva(r, dominio.Abortada)
	}
	return copiaReserva(r), nil
}

// Uma carona da reserva foi cancelada pelo motorista: a reserva inteira deixa de valer
func (e *Estado) InvalidaReserva(id string) (dominio.Reserva, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, existe := e.reservas[id]
	if !existe {
		return dominio.Reserva{}, errNaoEncontrada
	}
	switch r.Estado {
	case dominio.Preparando:
		e.mudaReserva(r, dominio.Abortada)
	case dominio.Confirmada:
		e.mudaReserva(r, dominio.Cancelada)
	}
	return copiaReserva(r), nil
}

// CancelarReserva cancela uma reserva confirmada do passageiro em todas as empresas
func (e *Estado) CancelarReserva(passageiro, reservaID string) (dominio.Reserva, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, existe := e.reservas[reservaID]
	if !existe || r.Estado != dominio.Confirmada {
		return dominio.Reserva{}, fmt.Errorf("Reserva nao encontrada")
	}
	if r.Passageiro != passageiro {
		return dominio.Reserva{}, fmt.Errorf("Reserva pertence a outro passageiro")
	}
	e.mudaReserva(r, dominio.Cancelada)
	return copiaReserva(r), nil
}

// Ao reiniciar: toda reserva que ficou sem decisão é abortada
func (e *Estado) AbortaReservasPendentes() []dominio.Reserva {
	e.mu.Lock()
	defer e.mu.Unlock()

	var lista []dominio.Reserva
	for _, r := range e.reservas {
		if r.Estado == dominio.Preparando {
			e.mudaReserva(r, dominio.Abortada)
			lista = append(lista, copiaReserva(r))
		}
	}
	return lista
}

// Devolve a situação de uma reserva coordenada por esta empresa
func (e *Estado) SituacaoReserva(id string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, existe := e.reservas[id]
	if !existe {
		return "", false
	}
	return r.Estado, true
}

// ListarReservas devolve todas as reservas confirmadas de um passageiro
func (e *Estado) ListarReservas(passageiro string) []dominio.Reserva {
	e.mu.Lock()
	defer e.mu.Unlock()

	var lista []dominio.Reserva
	for _, r := range e.reservas {
		if r.Passageiro == passageiro && r.Estado == dominio.Confirmada {
			lista = append(lista, copiaReserva(r))
		}
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].Relogio < lista[j].Relogio })
	return lista
}

// Muda o estado da reserva e marca todas as empresas envolvidas (inclusive
// esta) como pendentes de receber a decisão, numa única gravação
func (e *Estado) mudaReserva(r *dominio.Reserva, novoEstado string) {
	r.Estado = novoEstado
	r.Pendentes = empresasDaReserva(*r)
	e.arm.Grava(tipoReserva, r.ID, r)
}

// ---------- Mensagens pendentes ----------

// Ação enviada ao participante para cada decisão
var acaoDaDecisao = map[string]string{
	dominio.Confirmada: "confirmar",
	dominio.Abortada:   "abortar",
	dominio.Cancelada:  "cancelar",
}

// Devolve as mensagens ainda não entregues: as decisões que alguma empresa
// ainda não recebeu, e os avisos de carona cancelada que o coordenador ainda
// não aceitou
func (e *Estado) MensagensPendentes() []Mensagem {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mensagensPendentes()
}

func (e *Estado) mensagensPendentes() []Mensagem {
	var lista []Mensagem
	for _, r := range e.reservas {
		for _, empresa := range r.Pendentes {
			lista = append(lista, Mensagem{
				Reserva: r.ID,
				Empresa: empresa,
				Caminho: fmt.Sprintf("/participacoes/%s/%s", r.ID, acaoDaDecisao[r.Estado]),
			})
		}
	}
	for _, p := range e.participacoes {
		if (p.Estado == dominio.Preparada || p.Estado == dominio.Confirmada) &&
			!e.invalidacoesAceitas[p.ID] && e.usaCaronaInativa(p) {
			lista = append(lista, Mensagem{
				Reserva:     p.ID,
				Empresa:     p.Coordenador,
				Caminho:     "/reservas/" + p.ID + "/invalidacao",
				Invalidacao: true,
			})
		}
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].Caminho+lista[i].Empresa < lista[j].Caminho+lista[j].Empresa })
	return lista
}

// Indica se a participação usa alguma carona já cancelada pelo motorista
func (e *Estado) usaCaronaInativa(p *dominio.Participacao) bool {
	for _, item := range p.Itens {
		if c, existe := e.caronas[item.CaronaID]; existe && !c.Ativa {
			return true
		}
	}
	return false
}

// Registra que a mensagem foi entregue
func (e *Estado) MensagemEntregue(m Mensagem) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if m.Invalidacao {
		e.invalidacoesAceitas[m.Reserva] = true
		return
	}
	r, existe := e.reservas[m.Reserva]
	if !existe {
		return
	}
	restantes := r.Pendentes[:0:0]
	for _, empresa := range r.Pendentes {
		if empresa != m.Empresa {
			restantes = append(restantes, empresa)
		}
	}
	if len(restantes) != len(r.Pendentes) {
		r.Pendentes = restantes
		e.arm.Grava(tipoReserva, r.ID, r)
	}
}

// ---------- Auditoria ----------

// Auditoria é uma fotografia do estado, usada pelo teste com falhas
type Auditoria struct {
	Empresa       string                 `json:"empresa"`
	Caronas       []dominio.Carona       `json:"caronas"`
	Reservas      []dominio.Reserva      `json:"reservas"`
	Participacoes []dominio.Participacao `json:"participacoes"`
	Pendentes     int                    `json:"mensagens_pendentes"`
}

// Monta a fotografia do estado
func (e *Estado) Auditoria() Auditoria {
	e.mu.Lock()
	defer e.mu.Unlock()

	a := Auditoria{Empresa: e.empresa, Pendentes: len(e.mensagensPendentes())}
	for _, c := range e.caronas {
		copia := *c
		copia.AssentosLivres = append([]int(nil), c.AssentosLivres...)
		a.Caronas = append(a.Caronas, copia)
	}
	for _, r := range e.reservas {
		a.Reservas = append(a.Reservas, copiaReserva(r))
	}
	for _, p := range e.participacoes {
		a.Participacoes = append(a.Participacoes, copiaParticipacao(p))
	}
	return a
}

// ---------- Funções auxiliares ----------

// Empresas distintas envolvidas numa reserva
func empresasDaReserva(r dominio.Reserva) []string {
	vistas := make(map[string]bool)
	var lista []string
	for _, item := range r.Itens {
		if !vistas[item.Empresa] {
			vistas[item.Empresa] = true
			lista = append(lista, item.Empresa)
		}
	}
	return lista
}

func copiaItens(itens []dominio.ItemReserva) []dominio.ItemReserva {
	return append([]dominio.ItemReserva(nil), itens...)
}

func copiaReserva(r *dominio.Reserva) dominio.Reserva {
	copia := *r
	copia.Itens = copiaItens(r.Itens)
	return copia
}

func copiaParticipacao(p *dominio.Participacao) dominio.Participacao {
	copia := *p
	copia.Itens = copiaItens(p.Itens)
	return copia
}
