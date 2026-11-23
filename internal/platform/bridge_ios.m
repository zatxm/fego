// go:build ios

#include "_cgo_export.h"
#import <QuartzCore/QuartzCore.h>
#import <UIKit/UIKit.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <string.h>

extern void FegoStart(void);
static const int tileSize = 256;

@interface FegoTile : NSObject
@property(nonatomic, strong) CALayer *layer;
@property(nonatomic, assign) CGContextRef bitmap;
@property(nonatomic) BOOL dirty;
@end
@implementation FegoTile
- (void)dealloc {
    if (_bitmap)
        CGContextRelease(_bitmap);
}
@end

static void sendEvent(int kind, NSString *text, int mods, long long node) {
    NSData *data = [text dataUsingEncoding:NSUTF8StringEncoding];
    fe_event(kind, (char *)data.bytes, (int)data.length, mods, node);
}
@interface FegoElement : UIAccessibilityElement
@property(nonatomic) long long node;
@end
@implementation FegoElement
- (BOOL)accessibilityActivate {
    sendEvent(4, @"press", 0, self.node);
    return YES;
}
@end

@interface FegoSurface : UIView <UITextViewDelegate>
@property(nonatomic, strong) NSMutableDictionary<NSNumber *, FegoTile *> *tiles;
@property(nonatomic, strong) NSMutableArray<FegoTile *> *changed;
@property(nonatomic, strong) UITextView *editor;
@property(nonatomic, weak) UITouch *finger;
@property(nonatomic, weak) UIViewController *controller;
@property(nonatomic) BOOL finished;
@property(nonatomic) BOOL accessRequested;
@property(nonatomic) CGSize pixels;
@property(nonatomic) CGFloat lastScale;
@property(nonatomic) BOOL syncing, painting;
- (void)uploadX:(int)x
              y:(int)y
          width:(int)width
         height:(int)height
         stride:(int)stride
         pixels:(const char *)pixels;
@end

static __weak FegoSurface *surface;
static atomic_bool scheduled;

void fe_wake(void) {
    // Layout and input stay on UIKit's thread. Idle windows schedule no frames.
    if (atomic_exchange(&scheduled, true))
        return;
    dispatch_async(dispatch_get_main_queue(), ^{
      atomic_store(&scheduled, false);
      [surface setNeedsLayout];
    });
}
void fe_upload(int x, int y, int w, int h, int stride, const void *pixels) {
    [surface uploadX:x y:y width:w height:h stride:stride pixels:pixels];
}
static CGRect nodeRect(NSDictionary *n, CGFloat scale) {
    NSDictionary *a = n[@"Min"], *z = n[@"Max"];
    return CGRectMake([a[@"X"] doubleValue] / scale, [a[@"Y"] doubleValue] / scale,
                      ([z[@"X"] doubleValue] - [a[@"X"] doubleValue]) / scale,
                      ([z[@"Y"] doubleValue] - [a[@"Y"] doubleValue]) / scale);
}
static NSUInteger runeIndex(NSString *s, NSUInteger end) {
    NSUInteger count = 0;
    for (NSUInteger i = 0; i < MIN(end, s.length); i++, count++) {
        unichar c = [s characterAtIndex:i];
        if (CFStringIsSurrogateHighCharacter(c) && i + 1 < end && i + 1 < s.length &&
            CFStringIsSurrogateLowCharacter([s characterAtIndex:i + 1]))
            i++;
    }
    return count;
}
static NSUInteger utf16Index(NSString *s, NSUInteger count) {
    NSUInteger i = 0;
    while (i < s.length && count--) {
        unichar c = [s characterAtIndex:i++];
        if (CFStringIsSurrogateHighCharacter(c) && i < s.length &&
            CFStringIsSurrogateLowCharacter([s characterAtIndex:i]))
            i++;
    }
    return i;
}
@implementation FegoSurface
- (instancetype)initWithFrame:(CGRect)frame {
    if ((self = [super initWithFrame:frame])) {
        self.opaque = YES;
        self.clipsToBounds = YES;
        _tiles = [NSMutableDictionary new];
        _changed = [NSMutableArray new];
        _editor = [[UITextView alloc] initWithFrame:CGRectMake(0, 0, 2, 20)];
        UIPanGestureRecognizer *wheel = [[UIPanGestureRecognizer alloc] initWithTarget:self
                                                                                action:@selector(wheel:)];
        wheel.allowedScrollTypesMask = UIScrollTypeMaskAll;
        wheel.allowedTouchTypes = @[];
        [self addGestureRecognizer:wheel];
        [self addGestureRecognizer:[[UIHoverGestureRecognizer alloc] initWithTarget:self
                                                                             action:@selector(hover:)]];
        _editor.delegate = self;
        _editor.alpha = 0.02;
        _editor.textColor = UIColor.clearColor;
        _editor.tintColor = UIColor.clearColor;
        _editor.backgroundColor = UIColor.clearColor;
        _editor.isAccessibilityElement = NO;
        _editor.hidden = YES;
        [self addSubview:_editor];
    }
    return self;
}
// UIKit also queries this container on behalf of accessibility automation.
- (NSArray *)accessibilityElements {
    if (!self.accessRequested && self.window) {
        self.accessRequested = YES;
        [self setNeedsLayout];
        if (!self.painting)
            [self layoutIfNeeded];
    }
    return [super accessibilityElements];
}
- (BOOL)canBecomeFirstResponder {
    return YES;
}
- (void)didMoveToWindow {
    [super didMoveToWindow];
    if (self.window) {
        surface = self;
        self.lastScale = 0;
        [self becomeFirstResponder];
        [self setNeedsLayout];
    }
}
- (void)layoutSubviews {
    [super layoutSubviews];
    if (!self.window || surface != self)
        return;
    CGFloat scale = self.window.screen.scale;
    CGSize size = CGSizeMake(round(self.bounds.size.width * scale), round(self.bounds.size.height * scale));
    if (size.width <= 0 || size.height <= 0)
        return;
    BOOL reset = !CGSizeEqualToSize(size, self.pixels) || scale != self.lastScale;
    [CATransaction begin];
    [CATransaction setDisableActions:YES];
    if (reset) {
        for (FegoTile *t in self.tiles.allValues)
            [t.layer removeFromSuperlayer];
        [self.tiles removeAllObjects];
        [self.changed removeAllObjects];
        self.pixels = size;
        self.lastScale = scale;
    }
    self.painting = YES;
    BOOL accessibility = self.accessRequested || UIAccessibilityIsVoiceOverRunning() ||
                         UIAccessibilityIsSwitchControlRunning();
    char *state = fe_frame((int)size.width, (int)size.height, (float)scale, reset, accessibility);
    self.painting = NO;
    for (FegoTile *t in self.changed) {
        CGImageRef img = CGBitmapContextCreateImage(t.bitmap);
        if (img) {
            t.layer.contents = (__bridge id)img;
            CGImageRelease(img);
        }
        t.dirty = NO;
    }
    [self.changed removeAllObjects];
    [CATransaction commit];
    if (state) {
        NSData *data = [NSData dataWithBytesNoCopy:state length:strlen(state) freeWhenDone:YES];
        NSDictionary *s = [NSJSONSerialization JSONObjectWithData:data options:0 error:NULL];
        [self apply:s];
    }
}
- (void)uploadX:(int)x
              y:(int)y
          width:(int)width
         height:(int)height
         stride:(int)stride
         pixels:(const char *)pixels {
    for (int top = y; top < y + height;) {
        int ty = top / tileSize, dy = top % tileSize, h = MIN(tileSize - dy, y + height - top);
        for (int left = x; left < x + width;) {
            int tx = left / tileSize, dx = left % tileSize, w = MIN(tileSize - dx, x + width - left);
            NSNumber *key = @(((long long)ty << 32) | tx);
            FegoTile *t = self.tiles[key];
            if (!t) {
                int tw = MIN(tileSize, (int)self.pixels.width - tx * tileSize),
                    th = MIN(tileSize, (int)self.pixels.height - ty * tileSize);
                CGColorSpaceRef space = CGColorSpaceCreateDeviceRGB();
                CGContextRef ctx = CGBitmapContextCreate(
                    NULL, tw, th, 8, tw * 4, space, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
                CGColorSpaceRelease(space);
                if (!ctx)
                    return;
                t = [FegoTile new];
                t.bitmap = ctx;
                t.layer = [CALayer layer];
                CGFloat s = self.lastScale;
                t.layer.contentsScale = s;
                t.layer.frame = CGRectMake(tx * tileSize / s, ty * tileSize / s, tw / s, th / s);
                t.layer.opaque = YES;
                [self.layer insertSublayer:t.layer atIndex:0];
                self.tiles[key] = t;
            }
            // Release the previous snapshot before mutating the bitmap context.
            if (!t.dirty) {
                t.layer.contents = nil;
                t.dirty = YES;
                [self.changed addObject:t];
            }
            char *dst = CGBitmapContextGetData(t.bitmap);
            size_t pitch = CGBitmapContextGetBytesPerRow(t.bitmap);
            for (int row = 0; row < h; row++)
                memcpy(dst + (dy + row) * pitch + dx * 4, pixels + (top - y + row) * stride + (left - x) * 4,
                       w * 4);
            left += w;
        }
        top += h;
    }
    if (!self.painting)
        [self setNeedsLayout];
}
- (void)apply:(NSDictionary *)s {
    if (s[@"Title"])
        [self.window.rootViewController setTitle:s[@"Title"]];
    NSDictionary *input = s[@"Input"];
    if (input) {
        self.syncing = YES;
        if ([input[@"Active"] boolValue]) {
            BOOL secure = [input[@"Password"] boolValue];
            UIKeyboardType kb =
                [input[@"Numeric"] boolValue] ? UIKeyboardTypeNumberPad : UIKeyboardTypeDefault;
            BOOL changed = self.editor.secureTextEntry != secure || self.editor.keyboardType != kb;
            self.editor.secureTextEntry = secure;
            self.editor.keyboardType = kb;
            NSString *text = input[@"Text"];
            if (![self.editor.text isEqualToString:text])
                self.editor.text = text;
            NSUInteger a = utf16Index(text, [input[@"Start"] unsignedIntegerValue]),
                       b = utf16Index(text, [input[@"End"] unsignedIntegerValue]);
            NSRange sel = NSMakeRange(MIN(a, b), MAX(a, b) - MIN(a, b));
            if (!self.editor.markedTextRange && !NSEqualRanges(sel, self.editor.selectedRange))
                self.editor.selectedRange = sel;
            self.editor.frame = nodeRect(input[@"Caret"], self.lastScale);
            self.editor.hidden = NO;
            if (!self.editor.isFirstResponder)
                [self.editor becomeFirstResponder];
            else if (changed)
                [self.editor reloadInputViews];
        } else if (!self.editor.hidden) {
            [self.editor resignFirstResponder];
            self.editor.text = @"";
            self.editor.hidden = YES;
            [self becomeFirstResponder];
        }
        self.syncing = NO;
    }
    if (s[@"Access"]) {
        NSMutableArray *items = [NSMutableArray new];
        if ([s[@"Access"] isKindOfClass:NSArray.class])
            for (NSDictionary *n in s[@"Access"]) {
                NSString *role = n[@"Role"], *label = n[@"Label"], *value = n[@"Value"];
                if (!label.length && !value.length && [role isEqualToString:@"group"])
                    continue;
                FegoElement *e = [[FegoElement alloc] initWithAccessibilityContainer:self];
                e.node = [n[@"ID"] longLongValue];
                e.accessibilityLabel = label;
                e.accessibilityValue = value;
                e.accessibilityFrameInContainerSpace = nodeRect(n[@"Bounds"], self.lastScale);
                UIAccessibilityTraits traits = UIAccessibilityTraitNone;
                if ([role isEqualToString:@"text"])
                    traits |= UIAccessibilityTraitStaticText;
                if ([@[ @"button", @"switch", @"checkbox", @"radio" ] containsObject:role])
                    traits |= UIAccessibilityTraitButton;
                if ([n[@"Selected"] boolValue] || [n[@"Checked"] boolValue])
                    traits |= UIAccessibilityTraitSelected;
                if ([n[@"Disabled"] boolValue])
                    traits |= UIAccessibilityTraitNotEnabled;
                e.accessibilityTraits = traits;
                [items addObject:e];
            }
        self.accessibilityElements = items;
    }
    if (s[@"Copy"])
        UIPasteboard.generalPasteboard.string = s[@"Copy"];
    if ([s[@"Paste"] boolValue]) {
        NSString *text = UIPasteboard.generalPasteboard.string;
        if (text)
            sendEvent(2, text, 0, 0);
    }
    if ([s[@"Finish"] boolValue]) {
        [self.editor resignFirstResponder];
        self.finished = YES;
        self.userInteractionEnabled = NO;
        for (FegoTile *tile in self.tiles.allValues)
            [tile.layer removeFromSuperlayer];
        [self.tiles removeAllObjects];
        [self.changed removeAllObjects];
        if (self.controller.presentingViewController)
            [self.controller dismissViewControllerAnimated:YES completion:nil];
        else if (self.controller.navigationController.viewControllers.count > 1)
            [self.controller.navigationController popViewControllerAnimated:YES];
    }
}
- (void)editChanged {
    if (self.syncing || self.editor.hidden)
        return;
    NSString *s = self.editor.text;
    NSData *d = [s dataUsingEncoding:NSUTF8StringEncoding];
    NSRange sel = self.editor.selectedRange;
    if (sel.location == NSNotFound)
        return;
    fe_edit((char *)d.bytes, (int)d.length, (int)runeIndex(s, sel.location),
            (int)runeIndex(s, NSMaxRange(sel)));
}
- (BOOL)textView:(UITextView *)view shouldChangeTextInRange:(NSRange)range replacementText:(NSString *)text {
    if (![text isEqualToString:@"\n"] || view.markedTextRange)
        return YES;
    sendEvent(0, @"Enter", 0, 0);
    sendEvent(1, @"Enter", 0, 0);
    return NO;
}
- (void)textViewDidChange:(UITextView *)view {
    [self editChanged];
}
- (void)textViewDidChangeSelection:(UITextView *)view {
    [self editChanged];
}
- (void)wheel:(UIPanGestureRecognizer *)g {
    CGPoint d = [g translationInView:self], p = [g locationInView:self];
    if (d.x || d.y)
        fe_pointer(4, p.x * self.lastScale, p.y * self.lastScale, -d.x * self.lastScale,
                   -d.y * self.lastScale, 0);
    [g setTranslation:CGPointZero inView:self];
}
- (void)hover:(UIHoverGestureRecognizer *)g {
    CGPoint p = [g locationInView:self];
    fe_pointer(1, p.x * self.lastScale, p.y * self.lastScale, 0, 0, 0);
}
- (void)fingerEvent:(int)kind {
    CGPoint p = [self.finger locationInView:self];
    if (self.finger.type == UITouchTypeIndirectPointer)
        fe_pointer(kind, p.x * self.lastScale, p.y * self.lastScale, 0, 0, 0);
    else
        fe_touch(kind, p.x * self.lastScale, p.y * self.lastScale);
}
- (void)touchesBegan:(NSSet<UITouch *> *)set withEvent:(UIEvent *)event {
    if (self.finger)
        return;
    self.finger = set.anyObject;
    [self fingerEvent:0];
}
- (void)touchesMoved:(NSSet<UITouch *> *)set withEvent:(UIEvent *)event {
    if ([set containsObject:self.finger]) {
        [self fingerEvent:1];
    }
}
- (void)touchesEnded:(NSSet<UITouch *> *)set withEvent:(UIEvent *)event {
    if ([set containsObject:self.finger]) {
        [self fingerEvent:2];
        self.finger = nil;
    }
}
- (void)touchesCancelled:(NSSet<UITouch *> *)set withEvent:(UIEvent *)event {
    if ([set containsObject:self.finger]) {
        [self fingerEvent:3];
        self.finger = nil;
    }
}
- (void)keys:(NSSet<UIPress *> *)presses down:(BOOL)down {
    NSDictionary *names = @{
        UIKeyInputLeftArrow : @"ArrowLeft",
        UIKeyInputRightArrow : @"ArrowRight",
        UIKeyInputUpArrow : @"ArrowUp",
        UIKeyInputDownArrow : @"ArrowDown",
        @"\t" : @"Tab",
        @"\r" : @"Enter",
        @" " : @"Space",
        @"\b" : @"Backspace",
        UIKeyInputEscape : @"Escape"
    };
    for (UIPress *p in presses) {
        UIKey *k = p.key;
        if (!k)
            continue;
        NSString *text = k.charactersIgnoringModifiers;
        UIKeyModifierFlags m = k.modifierFlags;
        int flags = ((m & UIKeyModifierShift) ? 1 : 0) | ((m & UIKeyModifierControl) ? 2 : 0) |
                    ((m & UIKeyModifierAlternate) ? 4 : 0) | ((m & UIKeyModifierCommand) ? 8 : 0);
        sendEvent(down ? 0 : 1, names[text] ?: text, flags, 0);
    }
}
- (void)pressesBegan:(NSSet<UIPress *> *)p withEvent:(UIPressesEvent *)e {
    [self keys:p down:YES];
}
- (void)pressesEnded:(NSSet<UIPress *> *)p withEvent:(UIPressesEvent *)e {
    [self keys:p down:NO];
}
@end

@interface FegoController : UIViewController
@property(nonatomic, weak) FegoSurface *fegoView;
@end
@implementation FegoController
- (void)viewDidLoad {
    [super viewDidLoad];
    FegoSurface *v = [FegoSurface new];
    v.controller = self;
    self.fegoView = v;
    v.translatesAutoresizingMaskIntoConstraints = NO;
    [self.view addSubview:v];
    UILayoutGuide *safe = self.view.safeAreaLayoutGuide;
    [NSLayoutConstraint activateConstraints:@[
        [v.topAnchor constraintEqualToAnchor:safe.topAnchor],
        [v.leadingAnchor constraintEqualToAnchor:safe.leadingAnchor],
        [v.trailingAnchor constraintEqualToAnchor:safe.trailingAnchor],
        [v.bottomAnchor constraintEqualToAnchor:self.view.keyboardLayoutGuide.topAnchor]
    ]];
    surface = v;
    FegoStart();
}

- (void)viewDidAppear:(BOOL)animated {
    [super viewDidAppear:animated];
    NSNotificationCenter *center = NSNotificationCenter.defaultCenter;
    [center removeObserver:self];
    UIWindowScene *scene = self.view.window.windowScene;
    [center addObserver:self
               selector:@selector(suspend:)
                   name:scene ? UISceneWillDeactivateNotification : UIApplicationWillResignActiveNotification
                 object:scene];
    [center addObserver:self
               selector:@selector(resume:)
                   name:scene ? UISceneDidActivateNotification : UIApplicationDidBecomeActiveNotification
                 object:scene];
    if (scene)
        [center addObserver:self
                   selector:@selector(disconnect:)
                       name:UISceneDidDisconnectNotification
                     object:scene];
}
- (void)disconnect:(NSNotification *)note {
    if (surface == self.fegoView) {
        sendEvent(6, nil, 0, 0);
        surface = nil;
    }
}
- (void)viewDidDisappear:(BOOL)animated {
    [super viewDidDisappear:animated];
    if (surface == self.fegoView && (self.isBeingDismissed || self.isMovingFromParentViewController ||
                                     self.navigationController.isBeingDismissed)) {
        [self disconnect:nil];
    }
}
- (void)suspend:(NSNotification *)note {
    if (surface == self.fegoView)
        sendEvent(3, nil, 0, 0);
}
- (void)resume:(NSNotification *)note {
    if (surface != self.fegoView || !surface.window)
        return;
    if (surface.finished) {
        // A standalone UIKit app can reopen its closed session on foreground.
        surface.finished = NO;
        surface.userInteractionEnabled = YES;
        surface.lastScale = 0;
        FegoStart();
    }
    sendEvent(3, nil, 1, 0);
    fe_wake();
}
@end
UIViewController *FegoMobileViewController(void) { return [FegoController new]; }
