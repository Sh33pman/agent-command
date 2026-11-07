import CoreGraphics
import Foundation
import Testing

@testable import AgentCommand

private func check<T: Equatable>(_ got: T, _ want: T, _ what: String, sourceLocation: SourceLocation = #_sourceLocation) {
    #expect(got == want, "\(what)", sourceLocation: sourceLocation)
}

private func eq<T: Equatable>(_ what: String, _ got: T, _ want: T, sourceLocation: SourceLocation = #_sourceLocation) {
    #expect(got == want, "\(what)", sourceLocation: sourceLocation)
}

private func ids(_ t: PaneTree) -> [String] { t.leaves.map(\.id) }
private func sess(_ t: PaneTree) -> [String] { t.leaves.map { $0.session ?? "-" } }

@Test func paneTree() {

    var t = PaneTree(id: "a")
    check(ids(t), ["a"], "one pane"); check(t.focused, "a", "focus")
    t.show(session: "s1"); check(sess(t), ["s1"], "show")
    t.split(.right, session: "s2", newID: "b"); check(ids(t), ["a", "b"], "split"); check(t.focused, "b", "split focus")
    check(t.focusedSession ?? "-", "s2", "new pane shows its session")
    check(sess(t), ["s1", "s2"], "show in new")
    // Already showing: focuses, doesn't duplicate.
    t.show(session: "s1"); check(t.focused, "a", "focus existing"); check(sess(t), ["s1", "s2"], "no dup")
    t.split(.down, session: "s3", newID: "c")
    check(ids(t), ["a", "c", "b"], "nested order")
    check(t.visibleSessions, ["s1", "s2", "s3"], "visible")

    // Layout: 3 panes tile the area exactly, with dividers between.
    let area = CGRect(x: 0, y: 0, width: 1001, height: 801)
    let l = t.layout(in: area, thickness: 1)
    check(l.panes.count, 3, "panes"); check(l.dividers.count, 2, "dividers")
    let pa = l.panes.first { $0.id == "a" }!.rect, pc = l.panes.first { $0.id == "c" }!.rect, pb = l.panes.first { $0.id == "b" }!.rect
    check(pa.minY > pc.minY, true, "first is on top")
    check(pb.maxX, area.maxX, "right edge"); check(pb.height, area.height, "right full height")
    check(pa.minX, 0, "left edge"); check(pa.maxY, area.maxY, "top edge"); check(pc.minY, 0, "bottom edge")
    let total = l.panes.reduce(CGFloat(0)) { $0 + $1.rect.width * $1.rect.height } + l.dividers.reduce(CGFloat(0)) { $0 + $1.rect.width * $1.rect.height }
    check(total, area.width * area.height, "no gaps or overlaps")

    // Direction: c is under a; b is right of both.
    t.focus("c"); t.focus(.up); check(t.focused, "a", "up")
    t.focus(.down); check(t.focused, "c", "down")
    t.focus(.right); check(t.focused, "b", "right")
    t.focus(.left); check(["a", "c"].contains(t.focused), true, "left")
    t.focus("a"); t.focus(.left); check(t.focused, "a", "no pane to the left")

    // Ratio is clamped, and only the addressed split changes.
    var r = t; r.setRatio(at: [], to: 5)
    let wide = r.layout(in: area, thickness: 1).panes.first { $0.id == "a" }!.rect
    check(wide.width, (1000 * (1 - PaneTree.minRatio)).rounded(), "clamp high")
    r.setRatio(at: [false], to: 0.25)
    let lr = r.layout(in: area, thickness: 1)
    check(lr.panes.first { $0.id == "a" }!.rect.height, (800 * 0.25).rounded(), "nested ratio")
    r.equalize(); check(r.layout(in: area, thickness: 1).panes.first { $0.id == "a" }!.rect.width, 500, "equalize")

    // Closing keeps a neighbor focused, and collapses the split.
    var c = t; c.focus("c"); c.closeFocused()
    check(ids(c), ["a", "b"], "close"); check(c.layout(in: area, thickness: 1).dividers.count, 1, "collapsed")
    check(c.isValid(), true, "focus valid")
    c.closeFocused(); c.closeFocused()
    check(ids(c).count, 1, "last stays"); check(c.focusedSession ?? "-", "-", "last empty")
    c.closeFocused(); check(ids(c).count, 1, "closing again is safe")

    // Sessions that are gone close their panes.
    var k = t; k.keep(sessions: ["s2"]); check(sess(k), ["s2"], "keep"); check(k.isValid(), true, "keep focus")
    k.keep(sessions: []); check(sess(k), ["-"], "keep none")

    // Round trip through storage.
    let data = try! JSONEncoder().encode(t)
    let back = try! JSONDecoder().decode(PaneTree.self, from: data)
    check(back, t, "codable"); check(back.isValid(), true, "valid")
}

@Test func paneSets() {
    var p=PaneSets()
    // empty + split => alone, no set
    p.split(.right,session:"a"); eq("first alone",p.currentSetID,nil); eq("a focused",p.current.focusedSession,"a")
    // split creates set
    p.split(.right,session:"b"); let setID=p.currentSetID; eq("set made",setID != nil,true); eq("members",p.members,[["a","b"]])
    p.split(.down,session:"c"); eq("same set",p.currentSetID,setID); eq("members3",p.members,[["a","b","c"]]); eq("focus c",p.current.focusedSession,"c")
    // select in current focuses, no change
    p.select(session:"a"); eq("focus a",p.current.focusedSession,"a"); eq("still set",p.currentSetID,setID)
    // select standalone: set is parked intact
    let saved=p.current
    p.select(session:"z"); eq("z alone",p.current.visibleSessions,["z"]); eq("no set",p.currentSetID,nil); eq("parked",p.others.first?.tree,saved); eq("members kept",p.members,[["a","b","c"]])
    // select member returns whole set focused on member
    p.select(session:"b"); eq("back",p.currentSetID,setID); eq("all",p.current.visibleSessions,["a","b","c"]); eq("focus b",p.current.focusedSession,"b"); eq("no others",p.others.count,0)
    // second set, swap between
    p.select(session:"z"); p.split(.right,session:"y"); let s2=p.currentSetID; eq("second set",s2 != nil && s2 != setID,true); eq("two",p.members.count,2)
    p.select(session:"a"); eq("sw1",p.currentSetID,setID); eq("others",p.others.first?.id,s2)
    p.select(session:"y"); eq("sw2",p.currentSetID,s2); eq("others2",p.others.first?.id,setID)
    // moveOut from parked set
    p.moveOut(session:"b"); eq("parked shrink",p.others.first?.tree.visibleSessions,["a","c"])
    p.moveOut(session:"c"); eq("parked ended",p.others.count,0); eq("members 1",p.members,[["z","y"]])
    // moveOut current: two -> ends
    p.moveOut(session:"y"); eq("ended",p.currentSetID,nil); eq("z left",p.current.visibleSessions,["z"]); eq("no sets",p.members,[])
    // moveOut standalone: noop
    p.moveOut(session:"z"); eq("noop",p.current.visibleSessions,["z"])
    // close focused ends set, empty pane on last
    p.closeFocused(); eq("standalone close empties",p.current.focusedSession,nil); eq("leaf1",p.current.leaves.count,1)
    // keep
    p=PaneSets(); p.select(session:"a"); p.split(.right,session:"b"); p.split(.right,session:"c"); p.select(session:"q"); p.split(.down,session:"r")
    p.keep(sessions:["a","c","q","r"]); eq("keep1",p.others.first?.tree.visibleSessions,["a","c"])
    p.keep(sessions:["a","q"]); eq("keep2 others gone",p.others.count,0); eq("keep2 cur",p.currentSetID,nil); eq("q",p.current.visibleSessions,["q"])
    p.keep(sessions:[]); eq("all gone",p.current.focusedSession,nil)
    // round trip
    p=PaneSets(); p.select(session:"a"); p.split(.right,session:"b"); p.select(session:"q")
    let d=try! JSONEncoder().encode(p); eq("roundtrip",PaneSets.decode(d),p)
    // migration of old tree
    var t=PaneTree(); t.show(session:"a"); t.split(.right,session:"b"); t.split(.down,session:"c")
    var m=PaneSets.decode(try! JSONEncoder().encode(t))!; eq("mig set",m.currentSetID != nil,true); eq("mig members",m.members,[["a","b","c"]])
    var t1=PaneTree(); t1.show(session:"a"); m=PaneSets.decode(try! JSONEncoder().encode(t1))!; eq("mig single",m.currentSetID,nil)
    let old=#"{"root":{"split":{"direction":"right","ratio":0.5,"first":{"leaf":{"id":"x","session":"a"}},"second":{"leaf":{"id":"y"}}}},"focused":"y"}"#
    m=PaneSets.decode(old.data(using:.utf8)!)!; eq("mig empty dropped",m.current.leaves.count,1); eq("mig empty noset",m.currentSetID,nil); eq("mig focus",m.current.focusedSession,"a")
    eq("garbage",PaneSets.decode(Data("{}".utf8)),nil)
    // rows
    func r(_ o:[String],_ s:[[String]])->String{PaneSets.rows(order:o,sets:s).map{"\($0.id):\($0.link)"}.joined(separator:" ")}
    eq("rows",r(["a","b","c","d","e"],[["e","b"],["c","d"]]),"a:none e:first b:last c:first d:last")
    eq("rows mid",r(["a","b","c"],[["a","c","b"]]),"a:first c:middle b:last")
    eq("rows unlisted",r(["a","b"],[["a","x"]]),"a:none b:none")
    var q=PaneSets(); q.select(session:"a"); q.split(.right,session:"b"); q.split(.right,session:"c"); q.select(session:"b"); q.select(session:"z")
    q.showSet(containing:"a"); eq("showSet focus kept",q.current.focusedSession,"b"); eq("showSet is set",q.currentSetID != nil,true)
    q.showSet(containing:"c"); eq("showSet noop",q.current.focusedSession,"b")

    var h=PaneSets(); h.select(session:"a"); h.split(.right,session:"b"); h.split(.down,session:"c"); let hid=h.currentSetID
    h.closeFocused(); eq("hide keeps set",h.currentSetID,hid); eq("hide members",h.members,[["a","b","c"]]); eq("hide visible",h.current.visibleSessions,["a","b"]); eq("hide focus",h.current.focusedSession,"b")
    let fr=h.current.layout(in:CGRect(x:0,y:0,width:100,height:100),thickness:0); eq("hide frames",fr.panes.count,2); eq("hide dividers",fr.dividers.count,1); eq("b full height",fr.panes[1].rect.height,100)
    h.select(session:"c"); eq("c back",h.current.visibleSessions,["a","b","c"]); eq("c focus",h.current.focusedSession,"c"); eq("no new set",h.members.count,1)
    let rt=try! JSONEncoder().encode(h); h.closeFocused(); let rt2=try! JSONEncoder().encode(h); eq("hidden roundtrip",PaneSets.decode(rt2),h); _ = rt
    // hide all parks, empties; members remain
    h.closeFocused(); eq("one left",h.current.visibleSessions,["a"]); h.closeFocused(); eq("parked all",h.current.focusedSession,nil); eq("no cur set",h.currentSetID,nil); eq("still member",h.members,[["a","b","c"]]); eq("others1",h.others.count,1)
    // select one member brings only it
    var h2=h; h2.select(session:"b"); eq("only b",h2.current.visibleSessions,["b"]); eq("b set",h2.currentSetID,hid); eq("b mem",h2.members,[["a","b","c"]])
    // label brings all
    var h3=h; h3.showSet(containing:"a"); eq("label all",h3.current.visibleSessions,["a","b","c"]); eq("label set",h3.currentSetID,hid); eq("focus shown",h3.current.focusedSession != nil,true)
    // moving out a hidden member; set ends and lone shows
    var h4=PaneSets(); h4.select(session:"a"); h4.split(.right,session:"b"); h4.closeFocused(); eq("h4 a",h4.current.visibleSessions,["a"]); eq("h4 mem",h4.members,[["a","b"]])
    h4.moveOut(session:"a"); eq("lone hidden shows",h4.current.visibleSessions,["b"]); eq("h4 ended",h4.currentSetID,nil); eq("h4 focus",h4.current.focusedSession,"b")
    // hidden session archived
    var h5=PaneSets(); h5.select(session:"a"); h5.split(.right,session:"b"); h5.split(.right,session:"c"); h5.closeFocused(); h5.keep(sessions:["a","b"]); eq("h5",h5.members,[["a","b"]]); eq("h5 vis",h5.current.visibleSessions,["a","b"]); eq("h5 hid",h5.current.hidden.isEmpty,true)
    // visible moved out with others hidden: parked
    var h6=PaneSets(); h6.select(session:"a"); h6.split(.right,session:"b"); h6.split(.right,session:"c"); h6.closeFocused(); h6.closeFocused(); h6.moveOut(session:"a"); eq("h6 mem",h6.members,[["b","c"]]); eq("h6 empty",h6.current.focusedSession,nil)
    // split in a set with hidden pane keeps working
    var h7=PaneSets(); h7.select(session:"a"); h7.split(.right,session:"b"); h7.closeFocused(); h7.split(.down,session:"d"); eq("h7",h7.current.visibleSessions,["a","d"]); eq("h7 count",h7.members[0].count,3)
    // focus direction skips hidden
    var h8=PaneSets(); h8.select(session:"a"); h8.split(.right,session:"b"); h8.split(.right,session:"c"); h8.select(session:"b"); h8.closeFocused(); h8.select(session:"a"); h8.editCurrent{$0.focus(.right)}; eq("skip hidden",h8.current.focusedSession,"c")
    var g=PaneSets(); g.select(session:"a"); g.split(.right,session:"b"); g.split(.right,session:"c"); g.closeFocused(); eq("closed c",g.closedSessions,["c"])
    g.select(session:"z"); eq("closed parked",g.closedSessions,["c"]); g.select(session:"c"); eq("closed none",g.closedSessions,[]); g.select(session:"z"); eq("parked not closed",g.closedSessions,[])
}
