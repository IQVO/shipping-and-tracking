@rules
Feature: Label rules from the documentation
  A label is issued once per package of a purchase order. The carrier follows
  the tier, the address is read from customer-ordering only after the request
  itself is known to be good, checked for deliverability, and never leaves the
  encrypted snapshot (docs: use-cases IssueLabel and VoidLabel, aggregate
  design canvas, ADR 0002 A3/A9/A10/A25, ADR 0011).

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And customer order "ord-2" ships to the same address under another name

  # --- the carrier and the event ---------------------------------------------

  Scenario Outline: The carrier follows the service tier
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "<tier>"
    Then the label has carrier "<carrier>" and status "ISSUED"

    Examples:
      | tier     | carrier    |
      | SAME_DAY | RN-EXPRESS |
      | TWO_DAY  | RN-GROUND  |

  Scenario: LabelIssued announces the package with its order, assignment, tier and carrier
    When I issue a label for package 2 of "PO-1" for order "ord-1" on tier "TWO_DAY"
    Then the LabelIssued event for package 2 carries order "ord-1", assignment "asg-1", tier "TWO_DAY" and carrier "RN-GROUND"
    And the LabelIssued event carries postal prefix "01310"

  # --- validation and the order of checks ---------------------------------------

  Scenario Outline: Package numbers run from 1 to 2147483647
    When I issue a label for package <packageNo> of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request succeeds
    And the published events are "LabelIssued"

    Examples:
      | packageNo  |
      | 1          |
      | 2147483647 |

  Scenario Outline: A package number outside 1 to 2147483647 is refused
    When I issue a label for package <packageNo> of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request is refused with "validation-failed"
    And nothing is stored

    Examples:
      | packageNo  |
      | 0          |
      | 2147483648 |

  Scenario: A malformed request is refused before customer-ordering is asked
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "OVERNIGHT"
    Then the request is refused with "validation-failed"
    And customer-ordering was asked 0 times
    And nothing is stored

  Scenario: A duplicate package is refused before customer-ordering is asked
    Given a label has been issued for package 1 of "PO-1"
    And customer-ordering is unavailable
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request is refused with "label-already-issued"
    And customer-ordering was asked 1 time

  Scenario: Issuing is safe to retry once customer-ordering is back
    Given customer-ordering is unavailable
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request is refused with "ship-to-unavailable"
    When customer-ordering is available again
    And I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request succeeds
    And the published events are "LabelIssued"
    And a ship-to snapshot is stored
    And customer-ordering was asked 2 times

  # --- the ship-to address --------------------------------------------------------

  Scenario Outline: A suspect address is refused with its reason token and stores nothing
    Given customer order "ord-9" is addressed to recipient "<recipient>", line1 "<line1>", city "<city>", postal code "<postal>"
    When I issue a label for package 1 of "PO-9" for order "ord-9" on tier "NEXT_DAY"
    Then the request is refused with "address-suspect"
    And the refusal names only the reason "<reason>"
    And nothing is stored

    Examples:
      | recipient   | line1          | city      | postal    | reason                |
      | Test        | 25 Rua Augusta | Sao Paulo | 01305-000 | RECIPIENT_PLACEHOLDER |
      | Ana Example | Rua Augusta    | Sao Paulo | 01305-000 | STREET_MISSING_NUMBER |
      | Ana Example | TBD            | Sao Paulo | 01305-000 | STREET_PLACEHOLDER    |
      | Ana Example | 25 Rua Augusta | Unknown   | 01305-000 | CITY_PLACEHOLDER      |
      | Ana Example | 25 Rua Augusta | Sao Paulo | AB        | POSTAL_CODE_TOO_SHORT |

  Scenario: A three-character postal code is routable
    Given customer order "ord-9" is addressed to recipient "Ana Example", line1 "25 Rua Augusta", city "Sao Paulo", postal code "A1B"
    When I issue a label for package 1 of "PO-9" for order "ord-9" on tier "NEXT_DAY"
    Then the request succeeds
    And the LabelIssued event carries postal prefix "A1B"

  Scenario: A ship-to without a region is refused as invalid and stores nothing
    Given customer order "ord-9" ships to an address without a region
    When I issue a label for package 1 of "PO-9" for order "ord-9" on tier "NEXT_DAY"
    Then the request is refused with "validation-failed"
    And nothing is stored

  Scenario Outline: A ship-to with a blank required field is refused as invalid and stores nothing
    Given customer order "ord-9" is addressed to recipient "<recipient>", line1 "<line1>", city "<city>", postal code "<postal>"
    When I issue a label for package 1 of "PO-9" for order "ord-9" on tier "NEXT_DAY"
    Then the request is refused with "validation-failed"
    And nothing is stored

    Examples:
      | recipient   | line1          | city      | postal    |
      | (blank)     | 25 Rua Augusta | Sao Paulo | 01305-000 |
      | Ana Example | (blank)        | Sao Paulo | 01305-000 |
      | Ana Example | 25 Rua Augusta | (blank)   | 01305-000 |
      | Ana Example | 25 Rua Augusta | Sao Paulo | (blank)   |
      | Ana Example | 25 Rua Augusta | Sao Paulo | ---       |

  Scenario Outline: The postal prefix is the first five letters or digits of the postal code
    Given customer order "ord-9" is addressed to recipient "Ana Example", line1 "25 Rua Augusta", city "London", postal code "<postal>"
    When I issue a label for package 1 of "PO-9" for order "ord-9" on tier "NEXT_DAY"
    Then the LabelIssued event carries postal prefix "<prefix>"

    Examples:
      | postal    | prefix |
      | 01305-000 | 01305  |
      | sw1a 1aa  | SW1A1  |
      | ab-12     | AB12   |

  Scenario: Spelling and punctuation of the address do not change the stop
    Given customer order "ord-7" is addressed to recipient "Ana Example", line1 "Rua Augusta 100", city "Sao Paulo", postal code "01305-000"
    And customer order "ord-8" is addressed to recipient "Bia Example", line1 "RUA AUGUSTA, 100.", city "SAO PAULO", postal code "01305000"
    When I issue a label for package 1 of "PO-7" for order "ord-7" on tier "NEXT_DAY"
    And I issue a label for package 1 of "PO-8" for order "ord-8" on tier "NEXT_DAY"
    Then the two LabelIssued events carry the same stop key and postal prefix "01305"

  Scenario: A different house number is a different stop
    Given customer order "ord-7" is addressed to recipient "Ana Example", line1 "Rua Augusta 100", city "Sao Paulo", postal code "01305-000"
    And customer order "ord-8" is addressed to recipient "Ana Example", line1 "Rua Augusta 102", city "Sao Paulo", postal code "01305-000"
    When I issue a label for package 1 of "PO-7" for order "ord-7" on tier "NEXT_DAY"
    And I issue a label for package 1 of "PO-8" for order "ord-8" on tier "NEXT_DAY"
    Then the two LabelIssued events carry different stop keys

  # --- voiding ----------------------------------------------------------------------

  Scenario: A void reason of 500 characters is accepted
    Given a label has been issued for package 1 of "PO-1"
    When I void the label with a reason of 500 characters
    Then the request succeeds
    And the label has carrier "RN-EXPRESS" and status "VOIDED"

  Scenario Outline: A void without a reason or with more than 500 characters is refused
    Given a label has been issued for package 1 of "PO-1"
    When I void the label with a reason of <length> characters
    Then the request is refused with "validation-failed"
    And the label is still "ISSUED"
    And the journey is "LABELLED"
    And the published events are "LabelIssued"

    Examples:
      | length |
      | 0      |
      | 501    |

  Scenario: Voiding twice keeps the first reason
    Given a label has been issued for package 1 of "PO-1"
    And I void the label with reason "order cancelled"
    When I void the label with reason "customer changed address"
    Then the request succeeds
    And the label has void reason "order cancelled"

  Scenario: LabelVoided names the label, its package and the reason
    Given a label has been issued for package 1 of "PO-1"
    When I void the label with reason "order cancelled before pickup"
    Then the LabelVoided event carries reason "order cancelled before pickup" for the tracking number of the label
    And the published events are "LabelIssued,LabelVoided"

  Scenario: A refused void leaves the label issued and publishes nothing
    Given a label has been issued for package 1 of "PO-1"
    And I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    When I void the label with reason "too late"
    Then the request is refused with "void-after-handover"
    And the label is still "ISSUED"
    And the published events are "LabelIssued,PackageScanned,PackageHandedOver"

  Scenario: A delivered package's label cannot be voided
    Given a label has been issued for package 1 of "PO-1"
    And the package is out for delivery
    And I record a "DELIVERED" scan "d1" at hour 12
    When I void the label with reason "customer asked"
    Then the request is refused
    And the label is still "ISSUED"
    And the journey is "DELIVERED"
