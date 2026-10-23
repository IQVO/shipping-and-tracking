Feature: The Open Host journey query
  GET /journeys/{trackingNumber} (GetJourney) returns the customer-visible
  timeline: the state, every stored scan applied or not in the order they
  were recorded, and the handover and delivery instants. An unknown tracking
  number, an unknown label and a scan for an unknown journey are not-found.

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"

  Scenario: The timeline keeps every scan in the order it was recorded
    When I record a "PICKUP_DEPARTURE" scan "s1" at hour 5
    And I record a "DEPARTURE" scan "old" at hour 2
    Then the request is refused with "scan-out-of-order"
    When I record a "DEPARTURE" scan "s2" at hour 6
    Then the request succeeds
    And I query the journey of the package
    And the timeline state is "IN_TRANSIT"
    And the timeline has the scans "s1,old,s2" in the order they were recorded
    And the scan "s1" is applied
    And the scan "old" is not applied
    And the scan "s2" is applied
    And the timeline says the package was handed over at hour 5

  Scenario: A delivered timeline carries the delivery instant
    Given the package is out for delivery
    And I record a "DELIVERED" scan "d1" at hour 20
    Then the request succeeds
    And I query the journey of the package
    And the timeline state is "DELIVERED"
    And the timeline has 5 scans of which 5 applied
    And the timeline says the package was handed over at hour 1
    And the timeline says the package was delivered at hour 20

  Scenario: An unknown tracking number is not found
    When I query the journey of tracking number "RN000000000099"
    Then the request is refused with "not-found"

  Scenario: Voiding an unknown label is not found
    When I void the label "lbl-404" with reason "cleanup"
    Then the request is refused with "not-found"
    And the journey is "LABELLED"

  Scenario: A scan for an unknown journey is not found
    When I record a "DEPARTURE" scan "s1" at hour 1 on tracking number "RN000000000099"
    Then the request is refused with "not-found"
    And the journey is "LABELLED"
