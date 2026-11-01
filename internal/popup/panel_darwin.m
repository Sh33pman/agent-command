// acmd's reply popup: a floating, centered NSPanel (SPEC §8.8). Thin by
// design: Go decides what it shows (acmdPopupUpdate) and what its keys do
// (acmdPopupSetBindings); this file only draws and reports events.

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#include "_cgo_export.h"

static void emit(NSString *kind, NSString *text, int n) {
    goPopupEvent((char *)[kind UTF8String], (char *)[(text ?: @"") UTF8String], n);
}

@interface AcmdPanel : NSPanel
@end
@implementation AcmdPanel
- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)canBecomeMainWindow { return YES; }
- (void)cancelOperation:(id)sender { emit(@"close", nil, 0); }
@end

@interface AcmdController : NSObject <NSTextViewDelegate, NSWindowDelegate>
@property(strong) AcmdPanel *panel;
@property(strong) NSTextField *title, *meta, *counter, *note;
@property(strong) NSTextView *body, *input;
@property(strong) NSScrollView *inputScroll;
@property(strong) NSStackView *options;
@property(strong) NSButton *play;
@property(strong) NSArray *bindings;
@property(assign) BOOL canReply;
@end

static AcmdController *ctl;

static NSTextField *label(CGFloat size, NSFontWeight weight, NSColor *color) {
    NSTextField *l = [NSTextField labelWithString:@""];
    l.font = [NSFont systemFontOfSize:size weight:weight];
    l.textColor = color;
    l.lineBreakMode = NSLineBreakByTruncatingTail;
    l.translatesAutoresizingMaskIntoConstraints = NO;
    [l setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    return l;
}

static NSButton *button(NSString *title, NSString *kind) {
    NSButton *b = [NSButton buttonWithTitle:title target:ctl action:@selector(buttonPressed:)];
    b.identifier = kind;
    b.bezelStyle = NSBezelStyleRounded;
    return b;
}

static NSScrollView *scrolling(NSTextView *tv) {
    NSScrollView *s = [[NSScrollView alloc] init];
    s.hasVerticalScroller = YES;
    s.borderType = NSNoBorder;
    s.drawsBackground = NO;
    s.translatesAutoresizingMaskIntoConstraints = NO;
    tv.minSize = NSMakeSize(0, 0);
    tv.maxSize = NSMakeSize(FLT_MAX, FLT_MAX);
    tv.verticallyResizable = YES;
    tv.horizontallyResizable = NO;
    tv.autoresizingMask = NSViewWidthSizable;
    tv.textContainer.widthTracksTextView = YES;
    tv.textContainerInset = NSMakeSize(4, 6);
    s.documentView = tv;
    return s;
}

@implementation AcmdController
- (void)build {
    NSRect frame = NSMakeRect(0, 0, 720, 520);
    self.panel = [[AcmdPanel alloc] initWithContentRect:frame
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskResizable | NSWindowStyleMaskUtilityWindow
        backing:NSBackingStoreBuffered defer:NO];
    self.panel.title = @"acmd";
    self.panel.level = NSFloatingWindowLevel;
    self.panel.hidesOnDeactivate = NO;
    self.panel.releasedWhenClosed = NO;
    self.panel.becomesKeyOnlyIfNeeded = NO;
    self.panel.floatingPanel = YES;
    self.panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary;
    self.panel.minSize = NSMakeSize(420, 300);
    self.panel.delegate = self;

    self.title = label(15, NSFontWeightSemibold, NSColor.labelColor);
    self.counter = label(13, NSFontWeightMedium, NSColor.secondaryLabelColor);
    self.counter.alignment = NSTextAlignmentRight;
    [self.counter setContentCompressionResistancePriority:NSLayoutPriorityRequired forOrientation:NSLayoutConstraintOrientationHorizontal];
    self.meta = label(12, NSFontWeightRegular, NSColor.secondaryLabelColor);
    self.note = label(12, NSFontWeightRegular, NSColor.secondaryLabelColor);

    self.body = [[NSTextView alloc] init];
    self.body.editable = NO;
    self.body.selectable = YES;
    self.body.font = [NSFont systemFontOfSize:14];
    self.body.drawsBackground = NO;
    NSScrollView *bodyScroll = scrolling(self.body);

    self.options = [[NSStackView alloc] init];
    self.options.orientation = NSUserInterfaceLayoutOrientationHorizontal;
    self.options.spacing = 8;
    self.options.translatesAutoresizingMaskIntoConstraints = NO;

    self.input = [[NSTextView alloc] init];
    self.input.font = [NSFont systemFontOfSize:14];
    self.input.richText = NO;
    self.input.allowsUndo = YES;
    self.input.delegate = self;
    self.inputScroll = scrolling(self.input);
    self.inputScroll.borderType = NSBezelBorder;

    self.play = button(@"Play", @"play");
    NSStackView *buttons = [NSStackView stackViewWithViews:@[
        button(@"◀ Previous", @"prev"), button(@"Next ▶", @"next"), self.play,
        button(@"Skip", @"skip"), button(@"Close", @"close")]];
    buttons.spacing = 8;
    buttons.translatesAutoresizingMaskIntoConstraints = NO;

    NSView *c = self.panel.contentView;
    for (NSView *v in @[self.title, self.counter, self.meta, bodyScroll, self.options, self.inputScroll, buttons, self.note]) {
        [c addSubview:v];
    }
    CGFloat m = 16;
    [NSLayoutConstraint activateConstraints:@[
        [self.title.topAnchor constraintEqualToAnchor:c.topAnchor constant:m],
        [self.title.leadingAnchor constraintEqualToAnchor:c.leadingAnchor constant:m],
        [self.counter.firstBaselineAnchor constraintEqualToAnchor:self.title.firstBaselineAnchor],
        [self.counter.trailingAnchor constraintEqualToAnchor:c.trailingAnchor constant:-m],
        [self.counter.leadingAnchor constraintGreaterThanOrEqualToAnchor:self.title.trailingAnchor constant:12],
        [self.meta.topAnchor constraintEqualToAnchor:self.title.bottomAnchor constant:4],
        [self.meta.leadingAnchor constraintEqualToAnchor:c.leadingAnchor constant:m],
        [self.meta.trailingAnchor constraintEqualToAnchor:c.trailingAnchor constant:-m],
        [bodyScroll.topAnchor constraintEqualToAnchor:self.meta.bottomAnchor constant:10],
        [bodyScroll.leadingAnchor constraintEqualToAnchor:c.leadingAnchor constant:m],
        [bodyScroll.trailingAnchor constraintEqualToAnchor:c.trailingAnchor constant:-m],
        [self.options.topAnchor constraintEqualToAnchor:bodyScroll.bottomAnchor constant:10],
        [self.options.leadingAnchor constraintEqualToAnchor:c.leadingAnchor constant:m],
        [self.options.trailingAnchor constraintLessThanOrEqualToAnchor:c.trailingAnchor constant:-m],
        [self.inputScroll.topAnchor constraintEqualToAnchor:self.options.bottomAnchor constant:10],
        [self.inputScroll.leadingAnchor constraintEqualToAnchor:c.leadingAnchor constant:m],
        [self.inputScroll.trailingAnchor constraintEqualToAnchor:c.trailingAnchor constant:-m],
        [self.inputScroll.heightAnchor constraintEqualToConstant:84],
        [buttons.topAnchor constraintEqualToAnchor:self.inputScroll.bottomAnchor constant:10],
        [buttons.trailingAnchor constraintEqualToAnchor:c.trailingAnchor constant:-m],
        [self.note.centerYAnchor constraintEqualToAnchor:buttons.centerYAnchor],
        [self.note.leadingAnchor constraintEqualToAnchor:c.leadingAnchor constant:m],
        [self.note.trailingAnchor constraintLessThanOrEqualToAnchor:buttons.leadingAnchor constant:-12],
        [buttons.bottomAnchor constraintEqualToAnchor:c.bottomAnchor constant:-m],
    ]];

    [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown handler:^NSEvent *(NSEvent *e) {
        NSString *action = [self actionFor:e];
        if (action) {
            emit(action, nil, 0);
            return nil;
        }
        // ⌘V anywhere in the panel pastes into the reply box.
        NSEventModifierFlags f = e.modifierFlags & (NSEventModifierFlagCommand | NSEventModifierFlagControl |
                                                    NSEventModifierFlagOption | NSEventModifierFlagShift);
        if (f == NSEventModifierFlagCommand && [[e.charactersIgnoringModifiers lowercaseString] isEqualToString:@"v"] &&
            self.canReply && self.panel.firstResponder != self.input) {
            [self.panel makeFirstResponder:self.input];
            [self.input setSelectedRange:NSMakeRange(self.input.string.length, 0)];
            [self.input pasteAsPlainText:nil];
            return nil;
        }
        return e;
    }];
}

// actionFor matches a key press against the bindings Go sent.
- (NSString *)actionFor:(NSEvent *)e {
    NSEventModifierFlags f = e.modifierFlags & NSEventModifierFlagDeviceIndependentFlagsMask;
    static NSDictionary *names;
    if (!names) {
        names = @{@(kVK_LeftArrow): @"left", @(kVK_RightArrow): @"right", @(kVK_UpArrow): @"up",
                  @(kVK_DownArrow): @"down", @(kVK_PageUp): @"pageup", @(kVK_PageDown): @"pagedown",
                  @(kVK_Escape): @"escape", @(kVK_Return): @"return", @(kVK_Space): @"space",
                  @(kVK_Tab): @"tab"};
    }
    NSString *key = names[@(e.keyCode)] ?: [e.charactersIgnoringModifiers lowercaseString];
    for (NSDictionary *b in self.bindings) {
        if (![b[@"key"] isEqualToString:key]) continue;
        NSEventModifierFlags want = 0;
        for (NSString *mod in b[@"mods"]) {
            if ([mod isEqualToString:@"cmd"]) want |= NSEventModifierFlagCommand;
            else if ([mod isEqualToString:@"ctrl"]) want |= NSEventModifierFlagControl;
            else if ([mod isEqualToString:@"alt"]) want |= NSEventModifierFlagOption;
            else if ([mod isEqualToString:@"shift"]) want |= NSEventModifierFlagShift;
        }
        NSEventModifierFlags have = f & (NSEventModifierFlagCommand | NSEventModifierFlagControl |
                                         NSEventModifierFlagOption | NSEventModifierFlagShift);
        if (have == want) return b[@"action"];
    }
    return nil;
}

- (void)buttonPressed:(NSButton *)b {
    if ([b.identifier hasPrefix:@"option:"]) {
        emit(@"option", nil, [[b.identifier substringFromIndex:7] intValue]);
        return;
    }
    emit(b.identifier, nil, 0);
}

// Enter sends the reply; Shift+Enter (or Option+Enter) is a new line.
- (BOOL)textView:(NSTextView *)tv doCommandBySelector:(SEL)sel {
    if (tv == self.input && sel == @selector(insertNewline:)) {
        NSEventModifierFlags f = NSApp.currentEvent.modifierFlags;
        if (f & (NSEventModifierFlagShift | NSEventModifierFlagOption)) {
            [tv insertNewlineIgnoringFieldEditor:nil];
            return YES;
        }
        NSString *text = [tv.string stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
        if (self.canReply && text.length > 0) emit(@"send", text, 0);
        return YES;
    }
    return NO;
}

- (BOOL)windowShouldClose:(NSWindow *)w {
    emit(@"close", nil, 0);
    return NO; // Go decides
}

- (void)apply:(NSDictionary *)s {
    BOOL visible = [s[@"visible"] boolValue];
    if (!visible) {
        if (self.panel.visible) {
            [self.panel orderOut:nil];
            [NSApp hide:nil]; // give focus back to the app the user was in
        }
        return;
    }
    NSString *oldTitle = self.title.stringValue;
    NSString *oldBody = self.body.string;
    self.title.stringValue = s[@"title"] ?: @"";
    self.meta.stringValue = s[@"meta"] ?: @"";
    self.counter.stringValue = s[@"counter"] ?: @"";
    self.note.stringValue = s[@"note"] ?: @"";
    NSString *body = s[@"body"] ?: @"";
    if (![body isEqualToString:oldBody]) {
        self.body.string = body;
        [self.body scrollRangeToVisible:NSMakeRange(0, 0)];
    }
    self.play.title = [s[@"playing"] boolValue] ? @"Stop" : @"Play";
    self.canReply = [s[@"canReply"] boolValue];
    self.input.editable = self.canReply;
    self.inputScroll.hidden = !self.canReply;
    if (![self.title.stringValue isEqualToString:oldTitle] || ![body isEqualToString:oldBody]) {
        self.input.string = @""; // another item: start a fresh reply
    }
    for (NSView *v in [self.options.arrangedSubviews copy]) [v removeFromSuperview];
    NSArray *opts = s[@"options"];
    for (NSUInteger i = 0; i < opts.count; i++) {
        NSString *t = [NSString stringWithFormat:@"%lu. %@", (unsigned long)i + 1, opts[i]];
        NSButton *b = button(t, [NSString stringWithFormat:@"option:%lu", (unsigned long)i]);
        [self.options addArrangedSubview:b];
    }
    self.options.hidden = opts.count == 0;
    if (!self.panel.visible) {
        NSScreen *screen = NSScreen.mainScreen;
        for (NSScreen *sc in NSScreen.screens) {
            if (NSMouseInRect(NSEvent.mouseLocation, sc.frame, NO)) screen = sc;
        }
        NSRect vf = screen.visibleFrame, f = self.panel.frame;
        [self.panel setFrameOrigin:NSMakePoint(NSMidX(vf) - f.size.width / 2, NSMidY(vf) - f.size.height / 2)];
        [NSApp activateIgnoringOtherApps:YES];
        [self.panel makeKeyAndOrderFront:nil];
        [self.panel makeFirstResponder:(self.canReply ? self.input : self.body)];
    }
}
@end

// Self-test (acmd popup --demo --selftest): posts real key events into the
// panel and checks copy and paste, then restores the clipboard.
static NSString *selfTestResult;

static void postKey(NSString *chars, unsigned short code, NSEventModifierFlags mods) {
    NSEvent *e = [NSEvent keyEventWithType:NSEventTypeKeyDown location:NSZeroPoint modifierFlags:mods
        timestamp:0 windowNumber:ctl.panel.windowNumber context:nil characters:chars
        charactersIgnoringModifiers:chars isARepeat:NO keyCode:code];
    [NSApp postEvent:e atStart:NO];
}

void acmdPopupSelfTest(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        NSPasteboard *pb = NSPasteboard.generalPasteboard;
        NSString *saved = [pb stringForType:NSPasteboardTypeString];
        NSMutableArray *results = [NSMutableArray array];
        [pb clearContents];
        [pb setString:@"pasted from the clipboard" forType:NSPasteboardTypeString];
        ctl.input.string = @"";
        [ctl.panel makeFirstResponder:ctl.body]; // focus on the response, not the reply box
        postKey(@"v", kVK_ANSI_V, NSEventModifierFlagCommand);
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 400 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
            [results addObject:[NSString stringWithFormat:@"paste-anywhere=%@",
                [ctl.input.string isEqualToString:@"pasted from the clipboard"] ? @"ok" : ctl.input.string]];
            [ctl.panel makeFirstResponder:ctl.body];
            postKey(@"a", kVK_ANSI_A, NSEventModifierFlagCommand);
            postKey(@"c", kVK_ANSI_C, NSEventModifierFlagCommand);
            dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 400 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
                NSString *copied = [pb stringForType:NSPasteboardTypeString] ?: @"";
                [results addObject:[NSString stringWithFormat:@"copy-response=%@",
                    [copied isEqualToString:ctl.body.string] ? @"ok" : [copied substringToIndex:MIN(copied.length, 40u)]]];
                [ctl.panel makeFirstResponder:ctl.input];
                ctl.input.string = @"reply text to copy";
                postKey(@"a", kVK_ANSI_A, NSEventModifierFlagCommand);
                postKey(@"c", kVK_ANSI_C, NSEventModifierFlagCommand);
                dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 400 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
                    NSString *c2 = [pb stringForType:NSPasteboardTypeString] ?: @"";
                    [results addObject:[NSString stringWithFormat:@"copy-reply=%@", [c2 isEqualToString:@"reply text to copy"] ? @"ok" : c2]];
                    postKey(@"x", kVK_ANSI_X, NSEventModifierFlagCommand);
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 300 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
                        [results addObject:[NSString stringWithFormat:@"cut-reply=%@", ctl.input.string.length == 0 ? @"ok" : ctl.input.string]];
                        [pb clearContents];
                        [pb setString:@"pasted in the reply box" forType:NSPasteboardTypeString];
                        [ctl.panel makeFirstResponder:ctl.input];
                        postKey(@"v", kVK_ANSI_V, NSEventModifierFlagCommand);
                        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 300 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
                            [results addObject:[NSString stringWithFormat:@"paste-in-reply=%@",
                                [ctl.input.string isEqualToString:@"pasted in the reply box"] ? @"ok" : ctl.input.string]];
                            [pb clearContents];
                            if (saved) [pb setString:saved forType:NSPasteboardTypeString];
                            selfTestResult = [results componentsJoinedByString:@" "];
                        });
                    });
                });
            });
        });
    });
}

const char *acmdPopupSelfTestResult(void) {
    __block NSString *s;
    dispatch_sync(dispatch_get_main_queue(), ^{ s = selfTestResult ?: @""; });
    return strdup([s UTF8String]);
}

const char *acmdFrontmostApp(void) {
    NSString *b = NSWorkspace.sharedWorkspace.frontmostApplication.bundleIdentifier ?: @"";
    return strdup([b UTF8String]);
}

const char *acmdPopupFrame(void) {
    __block NSString *s = @"";
    dispatch_sync(dispatch_get_main_queue(), ^{
        NSRect f = ctl.panel.frame, vf = ctl.panel.screen.visibleFrame;
        s = [NSString stringWithFormat:@"visible=%d key=%d frame=%.0f,%.0f %.0fx%.0f screen=%.0f,%.0f %.0fx%.0f level=%ld",
             ctl.panel.visible, ctl.panel.keyWindow, f.origin.x, f.origin.y, f.size.width, f.size.height,
             vf.origin.x, vf.origin.y, vf.size.width, vf.size.height, (long)ctl.panel.level];
    });
    return strdup([s UTF8String]);
}

// editMenu gives the panel the standard editing keys (⌘X ⌘C ⌘V ⌘A ⌘Z):
// AppKit routes them through the main menu, which a menu-bar-less
// (accessory) app doesn't have unless it's set here.
static void editMenu(void) {
    NSMenu *main = [[NSMenu alloc] init];
    NSMenuItem *appItem = [[NSMenuItem alloc] init];
    NSMenu *app = [[NSMenu alloc] initWithTitle:@"acmd"];
    [app addItemWithTitle:@"Close" action:@selector(performClose:) keyEquivalent:@"w"];
    appItem.submenu = app;
    [main addItem:appItem];
    NSMenuItem *editItem = [[NSMenuItem alloc] init];
    NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
    [edit addItemWithTitle:@"Undo" action:@selector(undo:) keyEquivalent:@"z"];
    NSMenuItem *redo = [edit addItemWithTitle:@"Redo" action:@selector(redo:) keyEquivalent:@"z"];
    redo.keyEquivalentModifierMask = NSEventModifierFlagCommand | NSEventModifierFlagShift;
    [edit addItem:[NSMenuItem separatorItem]];
    [edit addItemWithTitle:@"Cut" action:@selector(cut:) keyEquivalent:@"x"];
    [edit addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
    [edit addItemWithTitle:@"Paste" action:@selector(pasteAsPlainText:) keyEquivalent:@"v"];
    [edit addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
    editItem.submenu = edit;
    [main addItem:editItem];
    NSApp.mainMenu = main;
}

void acmdPopupRun(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        editMenu();
        ctl = [[AcmdController alloc] init];
        [ctl build];
        [NSApp run];
    }
}

void acmdPopupUpdate(const char *json) {
    NSData *d = [NSData dataWithBytes:json length:strlen(json)];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSDictionary *s = [NSJSONSerialization JSONObjectWithData:d options:0 error:nil];
        if ([s isKindOfClass:NSDictionary.class]) [ctl apply:s];
    });
}

void acmdPopupSetBindings(const char *json) {
    NSData *d = [NSData dataWithBytes:json length:strlen(json)];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSArray *b = [NSJSONSerialization JSONObjectWithData:d options:0 error:nil];
        if ([b isKindOfClass:NSArray.class]) ctl.bindings = b;
    });
}

static OSStatus hotKeyHandler(EventHandlerCallRef next, EventRef event, void *data) {
    EventHotKeyID hk;
    GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(hk), NULL, &hk);
    emit(@"hotkey", nil, (int)hk.id);
    return noErr;
}

int acmdPopupRegisterHotKey(int keyCode, int mods, int hotKeyID) {
    static BOOL installed;
    if (!installed) {
        EventTypeSpec spec = {kEventClassKeyboard, kEventHotKeyPressed};
        InstallApplicationEventHandler(&hotKeyHandler, 1, &spec, NULL, NULL);
        installed = YES;
    }
    EventHotKeyRef ref;
    EventHotKeyID hk = {'acmd', (UInt32)hotKeyID};
    return (int)RegisterEventHotKey((UInt32)keyCode, (UInt32)mods, hk, GetApplicationEventTarget(), 0, &ref);
}
