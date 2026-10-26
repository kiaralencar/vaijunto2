package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"vaijunto/internal/dominio"
)

// Intervalo das rotinas de fundo: reenvio de mensagens e verificação de prazos
const intervaloRotinas = 1 * time.Second

// Servidor junta o estado da empresa com tudo o que precisa de rede:
// a coordenação das reservas dos seus passageiros e a participação nas
// reservas que outras empresas pedem a ela.
type Servidor struct {
	cfg        Config
	estado     *Estado
	relogio    *Relogio
	vizinhos   *Vizinhos
	avisaSaida chan struct{}
}

// Monta o servidor e recupera o estado gravado em disco
func NovoServidor(cfg Config) (*Servidor, error) {
	arm, err := NovoArmazenamento(cfg.DirDados)
	if err != nil {
		return nil, err
	}
	relogio := &Relogio{}
	s := &Servidor{
		cfg:        cfg,
		relogio:    relogio,
		estado:     NovoEstado(cfg.Empresa, arm, relogio),
		vizinhos:   NovosVizinhos(cfg.Vizinhos),
		avisaSaida: make(chan struct{}, 1),
	}
	if err := s.estado.Carrega(); err != nil {
		return nil, fmt.Errorf("erro ao carregar dados: %v", err)
	}

	// Recuperação: reservas que estavam no meio da preparação quando a empresa caiu são abortadas
	for _, r := range s.estado.AbortaReservasPendentes() {
		fmt.Println("[recuperacao] reserva", r.ID, "estava sem decisao: abortada")
		s.propaga(r)
	}
	return s, nil
}

// Dispara as rotinas de fundo
func (s *Servidor) IniciaRotinas() {
	go s.vizinhos.Monitora(s)
	go s.rotinaSaida()
	go s.rotinaPrazos()
}

// ---------- Coordenador ----------

// Reservar coordena a reserva de um itinerário que pode envolver várias
// empresas: prepara em todas (fase 1) e só confirma se todas aceitarem (fase 2).
func (s *Servidor) Reservar(passageiro string, pedido []dominio.ItemReserva) (dominio.Reserva, error) {
	if len(pedido) == 0 {
		return dominio.Reserva{}, fmt.Errorf("nenhum item para reservar")
	}
	for i := range pedido {
		if pedido[i].Empresa == "" {
			pedido[i].Empresa = empresaDaCarona(pedido[i].CaronaID)
		}
		if pedido[i].Empresa == "" {
			return dominio.Reserva{}, fmt.Errorf("carona %s nao identificada", pedido[i].CaronaID)
		}
	}

	r := s.estado.CriaReserva(passageiro, pedido, s.relogio.Tick())

	// Fase 1: prepara em paralelo em cada empresa envolvida
	type voto struct {
		empresa string
		itens   []dominio.ItemReserva
		err     error
	}
	grupos := agrupaPorEmpresa(r.Itens)
	votos := make(chan voto, len(grupos))
	for empresa, itens := range grupos {
		go func(empresa string, itens []dominio.ItemReserva) {
			p := dominio.Participacao{
				ID:          r.ID,
				Coordenador: s.cfg.Empresa,
				Passageiro:  passageiro,
				Relogio:     r.Relogio,
				Itens:       itens,
			}
			preparados, err := s.preparaEm(empresa, p)
			votos <- voto{empresa, preparados, err}
		}(empresa, itens)
	}

	preparados := make(map[string][]dominio.ItemReserva)
	var primeiroErro error
	for range grupos {
		v := <-votos
		if v.err != nil && primeiroErro == nil {
			primeiroErro = v.err
		}
		preparados[v.empresa] = v.itens
	}

	if s.cfg.AtrasoDecisao > 0 {
		time.Sleep(s.cfg.AtrasoDecisao) // Janela para injeção de falhas nos testes
	}

	// Fase 2: decide e avisa todas as empresas
	var itensFinais []dominio.ItemReserva
	if primeiroErro == nil {
		itensFinais = juntaPorEmpresa(r.Itens, preparados)
	}
	decisao := s.estado.DecideReserva(r.ID, primeiroErro == nil, itensFinais)
	s.propaga(decisao)

	if decisao.Estado == dominio.Confirmada {
		return decisao, nil
	}
	if primeiroErro == nil {
		primeiroErro = fmt.Errorf("reserva perdeu a disputa para outra com precedencia")
	}
	return decisao, primeiroErro
}

// Pede a preparação a uma empresa (a própria é tratada sem rede)
func (s *Servidor) preparaEm(empresa string, p dominio.Participacao) ([]dominio.ItemReserva, error) {
	if empresa == s.cfg.Empresa {
		return s.preparaLocal(p)
	}
	var resp RespostaPreparacao
	status, err := s.requisicao(empresa, http.MethodPut, "/participacoes/"+p.ID, paraPedidoPreparacao(p), &resp)
	if err != nil {
		return nil, fmt.Errorf("empresa %s indisponivel", empresa)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return nil, fmt.Errorf("empresa %s recusou: %s", empresa, resp.Erro)
	}
	return deItensREST(resp.Itens), nil
}

// CancelarReserva cancela uma reserva confirmada em todas as empresas
func (s *Servidor) CancelarReserva(passageiro, reservaID string) error {
	r, err := s.estado.CancelarReserva(passageiro, reservaID)
	if err != nil {
		return err
	}
	s.propaga(r)
	return nil
}

// Entrega já, sem esperar a rotina de fundo, a decisão sobre a reserva à
// própria empresa, e acorda o envio às outras
func (s *Servidor) propaga(r dominio.Reserva) {
	for _, empresa := range r.Pendentes {
		if empresa == s.cfg.Empresa {
			s.entregaLocal(Mensagem{
				Reserva: r.ID,
				Empresa: empresa,
				Caminho: fmt.Sprintf("/participacoes/%s/%s", r.ID, acaoDaDecisao[r.Estado]),
			})
		}
	}
	s.acordaSaida()
}

// Uma carona desta empresa foi cancelada: os avisos aos coordenadores das
// reservas afetadas são gerados pelo estado e enviados pela rotina de fundo
func (s *Servidor) CancelarCarona(motorista, caronaID string) error {
	if err := s.estado.CancelarCarona(motorista, caronaID); err != nil {
		return err
	}
	s.acordaSaida()
	return nil
}

// ---------- Participante ----------

// Prepara a participação nesta empresa. Se faltar assento e o pedido tiver
// precedência (Lamport) sobre reservas ainda não confirmadas que ocupam o
// trecho, pede aos coordenadores delas que as abortem e tenta de novo.
func (s *Servidor) preparaLocal(p dominio.Participacao) ([]dominio.ItemReserva, error) {
	for tentativa := 0; ; tentativa++ {
		itens, candidatas, err := s.estado.PreparaParticipacao(p, s.cfg.PrazoDecisao)
		if err == nil {
			return itens, nil
		}
		if len(candidatas) == 0 || tentativa == 2 {
			return nil, err
		}
		liberou := false
		for _, q := range candidatas {
			if s.pedePreempcao(q) {
				s.estado.AbortaParticipacao(q.ID)
				liberou = true
			}
		}
		if !liberou {
			return nil, err
		}
	}
}

// Pede ao coordenador de uma reserva mais nova que a aborte, se ainda não decidiu
func (s *Servidor) pedePreempcao(q dominio.Participacao) bool {
	if q.Coordenador == s.cfg.Empresa {
		r, err := s.estado.Preempcao(q.ID)
		if err != nil {
			return true // Coordenador não conhece a reserva: nunca foi criada
		}
		s.propaga(r)
		return r.Estado == dominio.Abortada
	}
	var resp RespostaSituacao
	status, err := s.requisicao(q.Coordenador, http.MethodPost, "/reservas/"+q.ID+"/preempcao", nil, &resp)
	if err != nil {
		return false
	}
	return status == http.StatusNotFound || (status == http.StatusOK && resp.Estado == dominio.Abortada)
}

// Participações cujo prazo venceu sem decisão: pergunta ao coordenador o que foi decidido
func (s *Servidor) rotinaPrazos() {
	for {
		time.Sleep(intervaloRotinas)
		for _, p := range s.estado.ParticipacoesVencidas(time.Now()) {
			situacao, conhecida, err := s.consultaCoordenador(p)
			if err != nil {
				s.estado.AdiaParticipacao(p.ID, intervaloRotinas) // Coordenador fora do ar: tenta de novo
				continue
			}
			switch {
			case !conhecida || situacao == dominio.Abortada:
				s.estado.AbortaParticipacao(p.ID)
				fmt.Println("[prazo] reserva", p.ID, "abortada pelo coordenador: assentos liberados")
			case situacao == dominio.Confirmada:
				s.estado.ConfirmaParticipacao(p.ID)
				fmt.Println("[prazo] reserva", p.ID, "confirmada pelo coordenador")
			case situacao == dominio.Cancelada:
				s.estado.CancelaParticipacao(p.ID)
			default:
				s.estado.AdiaParticipacao(p.ID, s.cfg.PrazoDecisao) // Ainda preparando
			}
		}
	}
}

// Pergunta ao coordenador a situação da reserva
func (s *Servidor) consultaCoordenador(p dominio.Participacao) (string, bool, error) {
	if p.Coordenador == s.cfg.Empresa {
		situacao, conhecida := s.estado.SituacaoReserva(p.ID)
		return situacao, conhecida, nil
	}
	var resp RespostaSituacao
	status, err := s.requisicao(p.Coordenador, http.MethodGet, "/reservas/"+p.ID, nil, &resp)
	if err != nil {
		return "", false, err
	}
	switch status {
	case http.StatusOK:
		return resp.Estado, true, nil
	case http.StatusNotFound:
		return "", false, nil
	}
	return "", false, fmt.Errorf("status inesperado %d", status)
}

// ---------- Mensagens pendentes ----------

// Acorda a rotina de envio sem esperar o próximo intervalo
func (s *Servidor) acordaSaida() {
	select {
	case s.avisaSaida <- struct{}{}:
	default:
	}
}

// Reenvia as notificações pendentes até cada empresa responder
func (s *Servidor) rotinaSaida() {
	for {
		select {
		case <-s.avisaSaida:
		case <-time.After(intervaloRotinas):
		}
		var wg sync.WaitGroup
		for _, m := range s.estado.MensagensPendentes() {
			if m.Empresa == s.cfg.Empresa {
				s.entregaLocal(m)
				continue
			}
			wg.Add(1)
			go func(m Mensagem) {
				defer wg.Done()
				status, err := s.requisicao(m.Empresa, http.MethodPost, m.Caminho, nil, nil)
				// 404/409 também encerram: a outra empresa já resolveu a situação
				if err == nil && (status < 300 || status == http.StatusNotFound || status == http.StatusConflict) {
					s.estado.MensagemEntregue(m)
				}
			}(m)
		}
		wg.Wait()
	}
}

// Entrega uma mensagem destinada à própria empresa, sem passar pela rede
func (s *Servidor) entregaLocal(m Mensagem) {
	if m.Invalidacao {
		r, err := s.estado.InvalidaReserva(m.Reserva)
		s.estado.MensagemEntregue(m)
		if err == nil {
			s.propaga(r)
		}
		return
	}
	switch {
	case strings.HasSuffix(m.Caminho, "/confirmar"):
		s.estado.ConfirmaParticipacao(m.Reserva)
	case strings.HasSuffix(m.Caminho, "/abortar"):
		s.estado.AbortaParticipacao(m.Reserva)
	case strings.HasSuffix(m.Caminho, "/cancelar"):
		s.estado.CancelaParticipacao(m.Reserva)
	}
	s.estado.MensagemEntregue(m)
}

// ---------- Funções auxiliares ----------

// Deduz a empresa pelo ID da carona ("<empresa>-C<n>")
func empresaDaCarona(caronaID string) string {
	i := strings.LastIndex(caronaID, "-C")
	if i <= 0 {
		return ""
	}
	return caronaID[:i]
}

// Separa os itens por empresa, mantendo a ordem
func agrupaPorEmpresa(itens []dominio.ItemReserva) map[string][]dominio.ItemReserva {
	grupos := make(map[string][]dominio.ItemReserva)
	for _, item := range itens {
		grupos[item.Empresa] = append(grupos[item.Empresa], item)
	}
	return grupos
}

// Remonta o itinerário na ordem original com os itens devolvidos por cada empresa
func juntaPorEmpresa(original []dominio.ItemReserva, preparados map[string][]dominio.ItemReserva) []dominio.ItemReserva {
	proximo := make(map[string]int)
	var lista []dominio.ItemReserva
	for _, item := range original {
		grupo := preparados[item.Empresa]
		i := proximo[item.Empresa]
		if i < len(grupo) {
			item = grupo[i]
		}
		proximo[item.Empresa]++
		lista = append(lista, item)
	}
	return lista
}

// Erro de rede (vizinho inacessível ou timeout)
var errIndisponivel = errors.New("empresa indisponivel")
