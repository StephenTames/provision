package midlayer

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"

	"github.com/digitalrebar/provision/backend"
	dhcp "github.com/krolaw/dhcp4"
)

func xid(p dhcp.Packet) string {
	return fmt.Sprintf("xid 0x%x", binary.BigEndian.Uint32(p.XId()))
}

type StrategyFunc func(p dhcp.Packet, options dhcp.Options) string

type Strategy struct {
	Name     string
	GenToken StrategyFunc
}

func MacStrategy(p dhcp.Packet, options dhcp.Options) string {
	return p.CHAddr().String()
}

type DhcpHandler struct {
	waitGroup  *sync.WaitGroup
	closing    bool
	ifs        []string
	port       int
	conn       *ipv4.PacketConn
	bk         *backend.DataTracker
	cm         *ipv4.ControlMessage
	strats     []*Strategy
	publishers *backend.Publishers
}

func (h *DhcpHandler) buildOptions(p dhcp.Packet,
	l *backend.Lease,
	s *backend.Subnet,
	r *backend.Reservation) (dhcp.Options, time.Duration, net.IP) {
	var leaseTime uint32 = 7200
	if s != nil {
		leaseTime = uint32(s.LeaseTimeFor(l.Addr) / time.Second)
	}

	opts := make(dhcp.Options)
	srcOpts := map[int]string{}
	for c, v := range p.ParseOptions() {
		srcOpts[int(c)] = backend.ConvertByteToOptionValue(c, v)
		h.Debugf("Received option: %v: %v", c, srcOpts[int(c)])
	}
	rt := make([]byte, 4)
	binary.BigEndian.PutUint32(rt, leaseTime/2)
	rbt := make([]byte, 4)
	binary.BigEndian.PutUint32(rbt, leaseTime*3/4)
	opts[dhcp.OptionRenewalTimeValue] = rt
	opts[dhcp.OptionRebindingTimeValue] = rbt
	nextServer := h.respondFrom(l.Addr)
	if s != nil {
		for _, opt := range s.Options {
			if opt.Value == "" {
				h.Printf("Ignoring DHCP option %d with zero-length value", opt.Code)
				continue
			}
			c, v, err := opt.RenderToDHCP(srcOpts)
			if err != nil {
				h.Printf("Failed to render option %v: %v, %v", opt.Code, opt.Value, err)
				continue
			}
			opts[c] = v
		}
		if s.NextServer.IsGlobalUnicast() {
			nextServer = s.NextServer
		}
	}
	if r != nil {
		for _, opt := range r.Options {
			if opt.Value == "" {
				h.Printf("Ignoring DHCP option %d with zero-length value", opt.Code)
				continue
			}
			c, v, err := opt.RenderToDHCP(srcOpts)
			if err != nil {
				h.Printf("Failed to render option %v: %v, %v", opt.Code, opt.Value, err)
				continue
			}
			opts[c] = v
		}
		if r.NextServer.IsGlobalUnicast() {
			nextServer = r.NextServer
		}
	}
	return opts, time.Duration(leaseTime) * time.Second, nextServer
}

func (h *DhcpHandler) Strategy(name string) StrategyFunc {
	for i := range h.strats {
		if h.strats[i].Name == name {
			return h.strats[i].GenToken
		}
	}
	return nil
}

func (h *DhcpHandler) Printf(f string, args ...interface{}) {
	h.bk.Printf(f, args...)
}
func (h *DhcpHandler) Infof(f string, args ...interface{}) {
	h.bk.Infof("debugDhcp", f, args...)
}
func (h *DhcpHandler) Debugf(f string, args ...interface{}) {
	h.bk.Debugf("debugDhcp", f, args...)
}

func (h *DhcpHandler) nak(p dhcp.Packet, addr net.IP) dhcp.Packet {
	return dhcp.ReplyPacket(p, dhcp.NAK, addr, nil, 0, nil)
}

const (
	reqInit = iota
	reqSelecting
	reqInitReboot
	reqRenewing
)

func reqAddr(p dhcp.Packet, msgType dhcp.MessageType, options dhcp.Options) (addr net.IP, state int) {
	reqBytes, haveReq := options[dhcp.OptionRequestedIPAddress]
	if haveReq {
		addr = net.IP(reqBytes)
	} else {
		addr = p.CIAddr()
	}
	_, haveSI := options[dhcp.OptionServerIdentifier]
	state = reqInit
	switch msgType {
	case dhcp.Request:
		if haveSI {
			state = reqSelecting
		} else if haveReq {
			state = reqInitReboot
		} else {
			state = reqRenewing
		}
	}
	return
}

func (h *DhcpHandler) intf() *net.Interface {
	if h.cm == nil {
		return nil
	}
	iface, err := net.InterfaceByIndex(h.cm.IfIndex)
	if err != nil {
		h.Printf("Error looking up interface index %d: %v", h.cm.IfIndex, err)
	}
	return iface
}

func (h *DhcpHandler) listenAddrs() []*net.IPNet {
	res := []*net.IPNet{}
	iface := h.intf()
	if iface == nil {
		return res
	}
	addrs, err := iface.Addrs()
	if err != nil {
		h.Printf("Error getting addrs for interface %s: %v", iface.Name, err)
		return res
	}
	for _, addr := range addrs {
		ip, cidr, err := net.ParseCIDR(addr.String())
		if err == nil {
			cidr.IP = ip
			res = append(res, cidr)
		}
	}
	return res
}

func (h *DhcpHandler) listenIPs() []net.IP {
	addrs := h.listenAddrs()
	res := make([]net.IP, len(addrs))
	for i := range addrs {
		res[i] = addrs[i].IP.To4()
	}
	return res
}

func (h *DhcpHandler) respondFrom(testAddr net.IP) net.IP {
	addrs := h.listenAddrs()
	for _, addr := range addrs {
		if addr.Contains(testAddr) {
			return addr.IP.To4()
		}
	}
	// Well, this sucks.  Return the first address we listen on for this interface.
	if len(addrs) > 0 {
		return addrs[0].IP.To4()
	}
	// Well, this really sucks.  Return our global listen-on address
	return net.ParseIP(h.bk.OurAddress).To4()
}

func (h *DhcpHandler) listenOn(testAddr net.IP) bool {
	for _, addr := range h.listenAddrs() {
		if addr.Contains(testAddr) {
			return true
		}
	}
	return false
}

func (h *DhcpHandler) Serve() error {
	defer h.waitGroup.Done()
	defer h.conn.Close()
	buf := make([]byte, 16384) // account for non-Ethernet devices maybe being used.
	for {
		h.conn.SetReadDeadline(time.Now().Add(time.Second))
		h.cm = nil
		cnt, control, srcAddr, err := h.conn.ReadFrom(buf)
		if err, ok := err.(net.Error); ok && err.Timeout() {
			continue
		}
		if err != nil {
			return err
		}
		if cnt < 240 {
			continue
		}
		req := dhcp.Packet(buf[:cnt])
		if req.HLen() > 16 {
			continue
		}
		options := req.ParseOptions()
		var reqType dhcp.MessageType
		if t := options[dhcp.OptionDHCPMessageType]; len(t) != 1 {
			continue
		} else {
			reqType = dhcp.MessageType(t[0])
			if reqType < dhcp.Discover || reqType > dhcp.Inform {
				continue
			}
		}
		h.cm = control
		if len(h.ifs) > 0 {
			canProcess := false
			tgtIf := h.intf()
			for _, ifName := range h.ifs {
				if strings.TrimSpace(ifName) == tgtIf.Name {
					canProcess = true
					break
				}
			}
			if !canProcess {
				h.Infof("DHCP: Completly ignoring packet from %s", tgtIf.Name)
				continue
			}
		}

		if res := h.ServeDHCP(req, reqType, options); res != nil {
			// If IP not available, broadcast
			ipStr, portStr, err := net.SplitHostPort(srcAddr.String())
			if err != nil {
				return err
			}

			port, _ := strconv.Atoi(portStr)
			if req.GIAddr().Equal(net.IPv4zero) {
				if net.ParseIP(ipStr).Equal(net.IPv4zero) || req.Broadcast() {
					srcAddr = &net.UDPAddr{IP: net.IPv4bcast, Port: port}
				}
			} else {
				srcAddr = &net.UDPAddr{IP: req.GIAddr(), Port: port}
			}

			h.cm.Src = nil
			if _, e := h.conn.WriteTo(res, h.cm, srcAddr); e != nil {
				return e
			}
		}
	}
}

func (h *DhcpHandler) ServeDHCP(p dhcp.Packet, msgType dhcp.MessageType, options dhcp.Options) (res dhcp.Packet) {
	h.Infof("Received DHCP packet: type %s %s ciaddr %s yiaddr %s giaddr %s chaddr %s",
		msgType.String(),
		xid(p),
		p.CIAddr(),
		p.YIAddr(),
		p.GIAddr(),
		p.CHAddr().String())
	// need code to figure out which interface or relay it came from
	req, reqState := reqAddr(p, msgType, options)
	var err error
	switch msgType {
	case dhcp.Decline:
		d, unlocker := h.bk.LockEnts("leases", "reservations", "subnets")
		defer unlocker()
		leaseThing := d("leases").Find(backend.Hexaddr(req))
		if leaseThing == nil {
			h.Infof("%s: Asked to decline a lease we didn't issue by %s, ignoring", xid(p), req)
			return nil
		}
		lease := backend.AsLease(leaseThing)
		stratfn := h.Strategy(lease.Strategy)
		if stratfn != nil && stratfn(p, options) == lease.Token {
			h.Infof("%s: Lease for %s declined, invalidating.", xid(p), lease.Addr)
			lease.Invalidate()
			h.bk.Save(d, lease, nil)
		} else {
			h.Infof("%s: Received spoofed decline for %s, ignoring", xid(p), lease.Addr)
		}
		return nil
	case dhcp.Release:
		d, unlocker := h.bk.LockEnts("leases", "reservations", "subnets")
		defer unlocker()
		leaseThing := d("leases").Find(backend.Hexaddr(req))
		if leaseThing == nil {
			h.Infof("%s: Asked to release a lease we didn't issue by %s, ignoring", xid(p), req)
			return nil
		}
		lease := backend.AsLease(leaseThing)
		stratfn := h.Strategy(lease.Strategy)
		if stratfn != nil && stratfn(p, options) == lease.Token {
			h.Infof("%s: Lease for %s released, expiring.", xid(p), lease.Addr)
			lease.Expire()
			h.bk.Save(d, lease, nil)
		} else {
			h.Infof("%s: Received spoofed release for %s, ignoring", xid(p), lease.Addr)
		}
		return nil
	case dhcp.Request:
		serverBytes, ok := options[dhcp.OptionServerIdentifier]
		server := net.IP(serverBytes)
		if ok && !h.listenOn(server) {
			h.Infof("%s: Ignoring request for DHCP server %s", xid(p), net.IP(server))
			return nil
		}
		if !req.IsGlobalUnicast() {
			h.Infof("%s: NAK'ing invalid requested IP %s", xid(p), req)
			return h.nak(p, h.respondFrom(req))
		}
		var lease *backend.Lease
		var reservation *backend.Reservation
		var subnet *backend.Subnet
		for _, s := range h.strats {
			lease, subnet, reservation, err = backend.FindLease(h.bk, s.Name, s.GenToken(p, options), req)
			if err != nil {
				if lease != nil {
					h.Infof("%s: %s already leased to %s:%s: %s",
						xid(p),
						req,
						lease.Strategy,
						lease.Token,
						err)
				} else {
					h.Infof("%s: %s is no longer able to be leased: %s",
						xid(p),
						req,
						err)
				}
				return h.nak(p, h.respondFrom(req))
			}
			if lease != nil {
				break
			}
		}
		if lease == nil {
			if reqState == reqInitReboot {
				h.Infof("%s: No lease for %s in database, client in INIT-REBOOT.  Ignoring request.", xid(p), req)
				return nil
			} else {
				h.Infof("%s: No lease for %s in database, NAK'ing", xid(p), req)
				return h.nak(p, h.respondFrom(req))
			}
		}
		opts, duration, nextServer := h.buildOptions(p, lease, subnet, reservation)
		reply := dhcp.ReplyPacket(p, dhcp.ACK,
			h.respondFrom(lease.Addr),
			lease.Addr,
			duration,
			opts.SelectOrderOrAll(opts[dhcp.OptionParameterRequestList]))
		if nextServer.IsGlobalUnicast() {
			reply.SetSIAddr(nextServer)
		}
		h.Infof("%s: Request handing out: %s to %s via %s", xid(p), reply.YIAddr(), reply.CHAddr(), h.respondFrom(lease.Addr))
		return reply
	case dhcp.Discover:
		for _, s := range h.strats {
			strat := s.Name
			token := s.GenToken(p, options)
			via := []net.IP{p.GIAddr()}
			if via[0] == nil || via[0].IsUnspecified() {
				via = h.listenIPs()
			}
			lease, subnet, reservation := backend.FindOrCreateLease(h.bk, strat, token, req, via)
			if lease != nil {
				opts, duration, _ := h.buildOptions(p, lease, subnet, reservation)
				reply := dhcp.ReplyPacket(p, dhcp.Offer,
					h.respondFrom(lease.Addr),
					lease.Addr,
					duration,
					opts.SelectOrderOrAll(opts[dhcp.OptionParameterRequestList]))
				h.Infof("%s: Discovery handing out: %s to %s via %s", xid(p), reply.YIAddr(), reply.CHAddr(), h.respondFrom(lease.Addr))
				return reply
			}
		}
	}
	return nil
}

func (h *DhcpHandler) Shutdown(ctx context.Context) error {
	h.Printf("Shutting down DHCP handler")
	h.closing = true
	h.conn.Close()
	h.waitGroup.Wait()
	h.Printf("DHCP handler shut down")
	return nil
}

type Service interface {
	Shutdown(context.Context) error
}

func StartDhcpHandler(dhcpInfo *backend.DataTracker, dhcpIfs string, dhcpPort int, pubs *backend.Publishers) (Service, error) {
	ifs := []string{}
	if dhcpIfs != "" {
		ifs = strings.Split(dhcpIfs, ",")
	}
	handler := &DhcpHandler{
		waitGroup:  &sync.WaitGroup{},
		ifs:        ifs,
		bk:         dhcpInfo,
		port:       dhcpPort,
		strats:     []*Strategy{&Strategy{Name: "MAC", GenToken: MacStrategy}},
		publishers: pubs,
	}

	l, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", handler.port))
	if err != nil {
		return nil, err
	}
	handler.conn = ipv4.NewPacketConn(l)
	if err := handler.conn.SetControlMessage(ipv4.FlagInterface, true); err != nil {
		l.Close()
		return nil, err
	}
	handler.waitGroup.Add(1)
	go func() {
		err := handler.Serve()
		if !handler.closing {
			dhcpInfo.Logger.Fatalf("DHCP handler died: %v", err)
		}
	}()
	return handler, nil
}
